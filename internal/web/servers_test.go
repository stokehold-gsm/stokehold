package web

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"stokehold/internal/helper"
	"stokehold/internal/store"
)

type fakeHelper struct {
	calls []helper.Request
	out   string
	ok    bool
	err   error
}

func (f *fakeHelper) call(r helper.Request) (helper.Response, error) {
	f.calls = append(f.calls, r)
	return helper.Response{OK: f.ok, Out: f.out}, f.err
}

func (r *rig) withHelper(f *fakeHelper) {
	r.s.Helper = f.call
}

func (r *rig) loginAs(emailAddr, password string) {
	r.t.Helper()
	r.jar = nil
	if rec := r.do("POST", "/login", url.Values{"email": {emailAddr}, "password": {password}}, nil); rec.Code != 303 {
		r.t.Fatalf("login %s: %d", emailAddr, rec.Code)
	}
}

func (r *rig) addServerRow() int64 {
	r.t.Helper()
	id, err := r.st.AddServer(store.Server{Name: "factorio", Game: "fctrserver", LinuxUser: "factorio", Dir: "/home/factorio", Mode: "adopted"})
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func idPath(id int64) string { return "/servers/" + string(rune('0'+id)) + "/run" }

func TestOwnerRunsActionsThroughTheHelper(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	f := &fakeHelper{ok: true, out: "Status: STARTED\n"}
	r.withHelper(f)
	tok := r.csrf()
	rec := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {"restart"}}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "STARTED") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := helper.Request{Op: "run", User: "factorio", Game: "fctrserver", Cmd: "restart"}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0], want) {
		t.Errorf("helper got %+v, want %+v", f.calls, want)
	}
	if r.st.AuditCount("server.run") != 1 {
		t.Error("the action was not audited")
	}
}

func TestViewerCanOnlyReadAndNeverStopsAServer(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	id := r.addServerRow()
	f := &fakeHelper{ok: true, out: "ok"}
	r.withHelper(f)
	r.loginAs("viewer@example.com", pass)
	dash := r.do("GET", "/", nil, nil).Body.String()
	if strings.Contains(dash, `value="stop"`) || strings.Contains(dash, "/servers/add") {
		t.Error("a viewer must not even see control buttons")
	}
	if !strings.Contains(dash, `value="details"`) {
		t.Error("a viewer should still be able to read status")
	}
	tok := r.csrf()
	for _, cmd := range []string{"stop", "start", "restart", "backup", "update"} {
		if rec := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {cmd}}, nil); rec.Code != 403 {
			t.Errorf("viewer %s: %d", cmd, rec.Code)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a refused action reached the helper: %+v", f.calls)
	}
	if rec := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {"details"}}, nil); rec.Code != 200 {
		t.Errorf("viewer details: %d", rec.Code)
	}
	if rec := r.do("GET", "/servers/add", nil, nil); rec.Code != 403 {
		t.Errorf("viewer on the add page: %d", rec.Code)
	}
	if rec := r.do("POST", "/servers/find", url.Values{"csrf": {tok}, "user": {"factorio"}}, nil); rec.Code != 403 {
		t.Errorf("viewer searching accounts: %d", rec.Code)
	}
}

func TestActionInputIsValidated(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	f := &fakeHelper{ok: true}
	r.withHelper(f)
	tok := r.csrf()
	for _, cmd := range []string{"console", "install", "rm -rf /", "", "start; id"} {
		if rec := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {cmd}}, nil); rec.Code != 403 {
			t.Errorf("cmd %q: %d", cmd, rec.Code)
		}
	}
	if rec := r.do("POST", "/servers/999/run", url.Values{"csrf": {tok}, "cmd": {"details"}}, nil); rec.Code != 404 {
		t.Errorf("unknown server: %d", rec.Code)
	}
	if rec := r.do("POST", "/servers/abc/run", url.Values{"csrf": {tok}, "cmd": {"details"}}, nil); rec.Code != 404 {
		t.Errorf("non-numeric id: %d", rec.Code)
	}
	if rec := r.do("POST", idPath(id), url.Values{"cmd": {"details"}}, nil); rec.Code != 403 {
		t.Errorf("no csrf token: %d", rec.Code)
	}
	if len(f.calls) != 0 {
		t.Errorf("invalid requests reached the helper: %+v", f.calls)
	}
}

func TestOutputIsEscapedAndCleaned(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	r.withHelper(&fakeHelper{ok: true, out: "\x1b[32m<script>alert(1)</script>\x1b[0m \xff ok"})
	tok := r.csrf()
	body := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {"details"}}, nil).Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "\x1b") {
		t.Errorf("unescaped or uncleaned output: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the output to be shown, escaped")
	}
}

func TestLongOutputIsTruncated(t *testing.T) {
	if got := trim(strings.Repeat("x", 3*maxOutput)); len(got) > maxOutput+8 || !strings.HasPrefix(got, "…") {
		t.Errorf("len=%d prefix=%q", len(got), got[:4])
	}
	if got := trim("short"); got != "short" {
		t.Errorf("short output changed: %q", got)
	}
}

func TestHelperProblemsAreShownNotHidden(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	tok := r.csrf()
	r.withHelper(&fakeHelper{err: errors.New("dial unix: connection refused")})
	if body := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {"details"}}, nil).Body.String(); !strings.Contains(body, "not reachable") {
		t.Error("a missing helper must be reported")
	}
	r.withHelper(&fakeHelper{ok: false, out: "boom"})
	if body := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {"details"}}, nil).Body.String(); !strings.Contains(body, "The command failed") || !strings.Contains(body, "boom") {
		t.Error("a failed command must show its output")
	}
	r.s.Helper = nil
	if rec := r.do("POST", idPath(id), url.Values{"csrf": {tok}, "cmd": {"details"}}, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "not reachable") {
		t.Error("no helper configured must be reported, not crash")
	}
}

func TestAddingAServerAsksTheHelperTwice(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	f := &fakeHelper{ok: true, out: "fctrserver\nmcserver"}
	r.withHelper(f)
	tok := r.csrf()

	rec := r.do("POST", "/servers/find", url.Values{"csrf": {tok}, "user": {"factorio"}}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "fctrserver") || !strings.Contains(rec.Body.String(), "mcserver") {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}
	// a forged form naming a script the account does not have is refused
	rec = r.do("POST", "/servers/add", url.Values{"csrf": {tok}, "user": {"factorio"}, "game": {"rustserver"}, "name": {"x"}}, nil)
	if rec.Code != 400 {
		t.Errorf("forged game: %d", rec.Code)
	}
	if list, _ := r.st.Servers(); len(list) != 0 {
		t.Fatal("a forged server was stored")
	}
	if rec = r.do("POST", "/servers/add", url.Values{"csrf": {tok}, "user": {"factorio"}, "game": {"fctrserver"}, "name": {"Factorio main"}}, nil); rec.Code != 303 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	list, _ := r.st.Servers()
	if len(list) != 1 || list[0].Mode != "adopted" || list[0].Name != "Factorio main" {
		t.Errorf("stored: %+v", list)
	}
	if rec = r.do("POST", "/servers/add", url.Values{"csrf": {tok}, "user": {"factorio"}, "game": {"fctrserver"}, "name": {"factorio main"}}, nil); rec.Code != 409 {
		t.Errorf("duplicate name: %d", rec.Code)
	}
	// gs_ accounts created by the helper are "managed"
	r.do("POST", "/servers/add", url.Values{"csrf": {tok}, "user": {"gs_test"}, "game": {"mcserver"}, "name": {"mc"}}, nil)
	list, _ = r.st.Servers()
	if len(list) != 2 || list[1].Mode != "managed" {
		t.Errorf("gs_ account should be managed: %+v", list)
	}
}

func TestFindWhenTheHelperRefuses(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	r.withHelper(&fakeHelper{ok: false})
	tok := r.csrf()
	rec := r.do("POST", "/servers/find", url.Values{"csrf": {tok}, "user": {"root"}}, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Could not find") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}
