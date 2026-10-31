package web

import (
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/catalog"
	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

// stepHelper answers per operation and records every request.
type stepHelper struct {
	mu    sync.Mutex
	calls []helper.Request
	resp  map[string]helper.Response
	gate  chan struct{} // when set, "install" waits for it
}

func (h *stepHelper) call(r helper.Request) (helper.Response, error) {
	h.mu.Lock()
	h.calls = append(h.calls, r)
	resp, ok := h.resp[r.Op]
	gate := h.gate
	h.mu.Unlock()
	if r.Op == "install" && gate != nil {
		<-gate
	}
	if !ok {
		resp = helper.Response{OK: true}
	}
	return resp, nil
}

func (h *stepHelper) ops() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var s []string
	for _, c := range h.calls {
		s = append(s, c.Op)
	}
	return strings.Join(s, ",")
}

func waitJob(t *testing.T, r *rig, id int64) job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := r.s.jobs.snapshot(id); ok && j.Status != "running" {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the job did not finish")
	return job{}
}

func startInstall(r *rig, tok, game, name string) (int, string) {
	rec := r.do("POST", "/servers/new", url.Values{"csrf": {tok}, "game": {game}, "name": {name}}, nil)
	return rec.Code, rec.Header().Get("Location")
}

func newInstallRig(t *testing.T) (*rig, *stepHelper, string) {
	r := newRig(t)
	r.s.Entitlement = pro(10)
	r.s.Distro = "ubuntu-24.04"
	h := &stepHelper{resp: map[string]helper.Response{}}
	r.s.Helper = h.call
	r.setup()
	r.login("")
	return r, h, r.csrf()
}

func TestInstallRunsThreeStepsAndRegistersTheServer(t *testing.T) {
	r, h, tok := newInstallRig(t)
	code, loc := startInstall(r, tok, "mcserver", "My MC")
	if code != 303 || loc != "/jobs/1" {
		t.Fatalf("start: %d %s", code, loc)
	}
	j := waitJob(t, r, 1)
	if j.Status != "done" {
		t.Fatalf("status %s, log: %v", j.Status, j.Log)
	}
	if h.ops() != "create-user,deps,install" {
		t.Errorf("operations: %s", h.ops())
	}
	if h.calls[0].User != "gs_mymc" || h.calls[2].User != "gs_mymc" || h.calls[2].Game != "mcserver" {
		t.Errorf("requests: %+v", h.calls)
	}
	has := false
	for _, p := range h.calls[1].Packages {
		has = has || p == "openjdk-25-jre"
	}
	if !has {
		t.Errorf("the game's own packages are missing: %v", h.calls[1].Packages)
	}
	list, _ := r.st.Servers()
	if len(list) != 1 || list[0].Mode != "managed" || list[0].LinuxUser != "gs_mymc" || list[0].Dir != "/home/gs_mymc" || list[0].Name != "My MC" {
		t.Errorf("stored: %+v", list)
	}
	page := r.do("GET", "/jobs/1", nil, nil).Body.String()
	if !strings.Contains(page, "Done.") || strings.Contains(page, `http-equiv="refresh"`) {
		t.Error("a finished job page must say so and stop refreshing")
	}
	if r.st.AuditCount("server.install") != 1 {
		t.Error("install not audited")
	}
}

func TestRunningJobPageRefreshesItself(t *testing.T) {
	r, h, tok := newInstallRig(t)
	h.gate = make(chan struct{})
	startInstall(r, tok, "mcserver", "x")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(r.do("GET", "/jobs/1", nil, nil).Body.String(), "installing Minecraft") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	page := r.do("GET", "/jobs/1", nil, nil).Body.String()
	if !strings.Contains(page, `http-equiv="refresh"`) || !strings.Contains(page, "Installing.") {
		t.Errorf("running page: %s", page)
	}
	close(h.gate)
	waitJob(t, r, 1)
}

func TestOnlyOneInstallAtATime(t *testing.T) {
	r, h, tok := newInstallRig(t)
	h.gate = make(chan struct{})
	startInstall(r, tok, "mcserver", "first")
	if code, _ := startInstall(r, tok, "vhserver", "second"); code != 409 {
		t.Errorf("a second concurrent install: %d", code)
	}
	close(h.gate)
	waitJob(t, r, 1)
	if code, _ := startInstall(r, tok, "vhserver", "second"); code != 303 {
		t.Errorf("after the first finished: %d", code)
	}
	waitJob(t, r, 2)
}

func TestFailuresStopTheJobAndRegisterNothing(t *testing.T) {
	for _, c := range []struct {
		failAt string
		ops    string
	}{{"create-user", "create-user"}, {"deps", "create-user,deps"}, {"install", "create-user,deps,install"}} {
		r, h, tok := newInstallRig(t)
		h.resp[c.failAt] = helper.Response{OK: false, Err: "exit status 9", Out: "fetching GitHub x\nline one\nFAIL: something specific broke\n"}
		startInstall(r, tok, "mcserver", "x")
		j := waitJob(t, r, 1)
		if j.Status != "failed" {
			t.Errorf("%s: status %s", c.failAt, j.Status)
		}
		if h.ops() != c.ops {
			t.Errorf("%s: operations %s, want %s", c.failAt, h.ops(), c.ops)
		}
		if list, _ := r.st.Servers(); len(list) != 0 {
			t.Errorf("%s: a failed install was registered", c.failAt)
		}
		log := strings.Join(j.Log, "\n")
		if !strings.Contains(log, "something specific broke") || strings.Contains(log, "fetching GitHub") {
			t.Errorf("%s: log should show the reason without the noise:\n%s", c.failAt, log)
		}
		if page := r.do("GET", "/jobs/1", nil, nil).Body.String(); !strings.Contains(page, "install failed") {
			t.Errorf("%s: the page must say it failed", c.failAt)
		}
	}
}

func TestUnknownDistroSkipsPackagesButStillInstalls(t *testing.T) {
	r, h, tok := newInstallRig(t)
	r.s.Distro = ""
	startInstall(r, tok, "mcserver", "x")
	j := waitJob(t, r, 1)
	if j.Status != "done" || h.ops() != "create-user,install" {
		t.Errorf("%s %s", j.Status, h.ops())
	}
	if !strings.Contains(strings.Join(j.Log, "\n"), "skipped") {
		t.Error("the skipped package step should be explained")
	}
}

func TestInstallInputAndLimits(t *testing.T) {
	r, _, tok := newInstallRig(t)
	for name, c := range map[string]struct{ game, name string }{
		"unknown game":      {"nosuchserver", "x"},
		"empty game":        {"", "x"},
		"shell in the game": {"mcserver;id", "x"},
		"path in the game":  {"../mcserver", "x"},
		"too long a name":   {"mcserver", strings.Repeat("a", 61)},
	} {
		if code, _ := startInstall(r, tok, c.game, c.name); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	r.addServerRow() // name "factorio"
	if code, _ := startInstall(r, tok, "mcserver", "FACTORIO"); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}
	r.s.Entitlement = nil // back to Free: one server, already present
	if code, _ := startInstall(r, tok, "mcserver", "another"); code != 403 {
		t.Errorf("over the Free limit: %d", code)
	}
}

func TestViewersCannotInstall(t *testing.T) {
	r, h, _ := newInstallRig(t)
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	r.loginAs("viewer@example.com", pass)
	tok := r.csrf()
	if code, _ := startInstall(r, tok, "mcserver", "x"); code != 403 {
		t.Errorf("viewer install: %d", code)
	}
	if rec := r.do("GET", "/servers/new", nil, nil); rec.Code != 403 {
		t.Errorf("viewer on the install page: %d", rec.Code)
	}
	if len(h.calls) != 0 {
		t.Error("a refused request reached the helper")
	}
}

func TestAccountNamesAreSafeAndUnique(t *testing.T) {
	never := func(string) bool { return false }
	for in, want := range map[string]string{
		"My MC":                 "gs_mymc",
		"Мой сервер":            "gs_mc", // nothing ASCII left, falls back to the game
		"a b; rm -rf /":         "gs_abrmrf",
		"UPPER123":              "gs_upper123",
		strings.Repeat("z", 40): "gs_" + strings.Repeat("z", 18),
	} {
		if got := accountFor(in, "mc", never); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	taken := map[string]bool{"gs_mymc": true, "gs_mymc1": true}
	if got := accountFor("my mc", "mc", func(u string) bool { return taken[u] }); got != "gs_mymc2" {
		t.Errorf("collision: %s", got)
	}
	for _, name := range []string{"x", "My MC", strings.Repeat("z", 40), "Мой сервер"} {
		u := accountFor(name, "mc", never)
		if len(u) > 24 || !strings.HasPrefix(u, "gs_") {
			t.Errorf("%q gives an invalid account %q", name, u)
		}
		if err := helper.Validate(helper.Request{Op: "create-user", User: u}); err != nil {
			t.Errorf("the helper would refuse %q: %v", u, err)
		}
	}
}

func TestCatalogueIsUsable(t *testing.T) {
	if n := len(catalog.Games()); n < 100 {
		t.Fatalf("only %d games", n)
	}
	g, ok := catalog.Find("fctrserver")
	if !ok || g.Name != "Factorio" || g.Short != "fctr" {
		t.Errorf("%+v %v", g, ok)
	}
	for _, g := range catalog.Games() {
		if err := helper.Validate(helper.Request{Op: "install", User: "gs_x", Game: g.Script}); err != nil {
			t.Errorf("the helper would refuse the catalogue entry %q: %v", g.Script, err)
		}
	}
}
