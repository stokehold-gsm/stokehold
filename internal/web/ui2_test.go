package web

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/store"
)

func (r *rig) viewer() {
	r.t.Helper()
	tok := r.csrf()
	code, pw := r.createUser(tok, "viewer@example.com", "viewer")
	if code != 200 || pw == "" {
		r.t.Fatalf("create viewer: %d", code)
	}
	r.jar = nil
	r.loginAs("viewer@example.com", pw)
}

func TestServersListOwnerSeesAllActionsViewerOnlyReadOnly(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	r.addServerRow()
	owner := r.do("GET", "/", nil, nil).Body.String()
	for _, cmd := range []string{"start", "stop", "restart", "details", "backup", "check-update"} {
		if !strings.Contains(owner, `name="cmd" value="`+cmd+`"`) {
			t.Errorf("the owner lacks the %s action", cmd)
		}
	}
	for _, link := range []string{"/schedules", "/files"} {
		if !strings.Contains(owner, link) {
			t.Errorf("the owner lacks the %s link", link)
		}
	}
	r.viewer()
	view := r.do("GET", "/", nil, nil).Body.String()
	for _, cmd := range []string{"start", "stop", "restart", "backup"} {
		if strings.Contains(view, `name="cmd" value="`+cmd+`"`) {
			t.Errorf("a viewer must not get the %s action", cmd)
		}
	}
	for _, cmd := range []string{"details", "check-update"} {
		if !strings.Contains(view, `name="cmd" value="`+cmd+`"`) {
			t.Errorf("a viewer keeps the read-only %s action", cmd)
		}
	}
	if strings.Contains(view, "/files") {
		t.Error("a viewer must not see the file link")
	}
}

func TestServerRowButtonsFollowTheState(t *testing.T) {
	rows := srvRows(storeServers(3), map[int64]ServerStatus{
		1: {State: "STARTED", HasPlayers: true, Players: 3, Max: 10},
		2: {State: "STOPPED"},
	}, nil)
	if r := rows[0]; r.State != "started" || r.CanStart || !r.CanStop || !r.CanRestart || r.Players != "3/10" {
		t.Errorf("a running server can be stopped and restarted, not started: %+v", r)
	}
	if r := rows[1]; r.State != "stopped" || !r.CanStart || r.CanStop || r.CanRestart {
		t.Errorf("a stopped server can only be started: %+v", r)
	}
	if r := rows[2]; r.State != "unknown" || !r.CanStart || !r.CanStop || !r.CanRestart {
		t.Errorf("an unknown state keeps every button: %+v", r)
	}
}

func TestThemeCookieBecomesDataThemeOnTheServer(t *testing.T) {
	r := newRig(t)
	r.setup()
	if body := r.do("GET", "/login", nil, nil).Body.String(); strings.Contains(body, "data-theme=") {
		t.Error("no cookie means follow the system: no data-theme")
	}
	for _, theme := range []string{"dark", "light"} {
		body := r.do("GET", "/login", nil, map[string]string{"Cookie": "theme=" + theme}).Body.String()
		if !strings.Contains(body, `data-theme="`+theme+`"`) {
			t.Errorf("theme %s not applied before paint", theme)
		}
	}
	if body := r.do("GET", "/login", nil, map[string]string{"Cookie": "theme=\"><script>"}).Body.String(); strings.Contains(body, "<script>") {
		t.Error("an odd cookie must be ignored")
	}
	rec := r.do("GET", "/theme?set=dark", nil, map[string]string{"Referer": "http://example.com/license"})
	if rec.Code != 303 || rec.Header().Get("Location") != "/license" || !strings.Contains(rec.Header().Get("Set-Cookie"), "theme=dark") {
		t.Errorf("the no-JS switch: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Header().Get("Set-Cookie"))
	}
	if rec := r.do("GET", "/theme?set=purple", nil, nil); rec.Code != 400 {
		t.Errorf("bad theme: %d", rec.Code)
	}
}

func TestOnlyTheOwnerOpensAndSavesSettings(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	page := r.do("GET", "/settings", nil, nil).Body.String()
	for _, want := range []string{`name="require_2fa"`, `name="session_hours"`, `name="default_lang"`, "Panel version"} {
		if !strings.Contains(page, want) {
			t.Errorf("settings lack %s", want)
		}
	}
	tok := r.csrf()
	for _, bad := range []string{"0", "169", "abc", ""} {
		f := url.Values{"csrf": {tok}, "session_hours": {bad}}
		if rec := r.do("POST", "/settings", f, nil); rec.Code != 400 {
			t.Errorf("session hours %q: %d", bad, rec.Code)
		}
	}
	if rec := r.do("POST", "/settings", url.Values{"csrf": {tok}, "session_hours": {"6"}, "default_lang": {"fr"}}, nil); rec.Code != 400 {
		t.Errorf("unknown language: %d", rec.Code)
	}
	// last: once two-factor is required the owner is sent to /security until they have it
	form := url.Values{"csrf": {tok}, "session_hours": {"6"}, "default_lang": {"ru"}, "require_2fa": {"1"}}
	if rec := r.do("POST", "/settings", form, nil); rec.Code != 303 {
		t.Fatalf("save: %d", rec.Code)
	}
	if !r.s.require2FA() || r.s.sessionHours() != 6 {
		t.Error("the settings were not stored")
	}
	if r.st.AuditCount("settings.require-2fa") != 1 || r.st.AuditCount("settings.save") != 1 {
		t.Error("the change must be audited")
	}

	r2 := newRig(t)
	r2.setup()
	r2.login("")
	r2.viewer()
	if rec := r2.do("GET", "/settings", nil, nil); rec.Code != 403 {
		t.Errorf("a viewer opened the settings: %d", rec.Code)
	}
}

func TestRequiredTwoFactorSendsUsersWithoutItToSecurity(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	r.do("POST", "/settings", url.Values{"csrf": {tok}, "session_hours": {"12"}, "require_2fa": {"1"}}, nil)
	rec := r.do("GET", "/", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/security" {
		t.Fatalf("the owner has no 2FA yet and the rule applies to them: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if body := r.do("GET", "/security", nil, nil); body.Code != 200 || !strings.Contains(body.Body.String(), "requires two-factor") {
		t.Errorf("the security page must explain why: %d", body.Code)
	}
	if rec := r.do("GET", "/license", nil, nil); rec.Code != 303 {
		t.Errorf("other pages stay closed: %d", rec.Code)
	}
	if rec := r.do("GET", "/settings", nil, nil); rec.Code != 303 {
		t.Errorf("the settings page too: %d", rec.Code)
	}
}

func TestSessionLifetimeAndDefaultLanguageFollowTheSettings(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	r.do("POST", "/settings", url.Values{"csrf": {tok}, "session_hours": {"2"}, "default_lang": {"ru"}}, nil)
	r.jar = nil
	rec := r.do("POST", "/login", url.Values{"email": {email}, "password": {pass}}, nil)
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=7200") {
		t.Errorf("the new session must live 2 hours: %s", rec.Header().Get("Set-Cookie"))
	}
	r.jar = nil
	if body := r.do("GET", "/login", nil, nil).Body.String(); !strings.Contains(body, `<html lang="ru"`) {
		t.Error("a visitor with no cookie must get the owner's default language")
	}
	if body := r.do("GET", "/login", nil, map[string]string{"Cookie": "lang=en"}).Body.String(); !strings.Contains(body, `<html lang="en"`) {
		t.Error("the visitor's own choice wins")
	}
}

func TestInstallStepsAreReadFromTheLog(t *testing.T) {
	log := []string{
		"[1/3] creating account gs_x ...", "    ok",
		"[2/3] packages: none needed",
		"[3/3] installing Minecraft with LinuxGSM (this can take a while) ...",
	}
	j := job{Status: "running", Log: log, Start: time.Now().Add(-90 * time.Second)}
	steps := jobSteps("en", j)
	got := []string{steps[0].State, steps[1].State, steps[2].State}
	if got[0] != "done" || got[1] != "done" || got[2] != "run" {
		t.Fatalf("states %v", got)
	}
	if pct := jobProgress(steps, "running"); pct != 83 {
		t.Errorf("two steps done and one running is 83%%, got %d", pct)
	}
	if e := elapsed(j, time.Now()); e != "1:30" {
		t.Errorf("elapsed %q", e)
	}

	j.Status, j.Log = "failed", append(log[:3:3], "[3/3] installing Minecraft ...", "    failed: download error")
	steps = jobSteps("en", j)
	if steps[2].State != "fail" || steps[2].Note != "download error" || jobFailure(steps, j) != "download error" {
		t.Errorf("a failed step must carry its error: %+v", steps[2])
	}
	if jobProgress(steps, "failed") != 66 {
		t.Errorf("progress stops at the failure: %d", jobProgress(steps, "failed"))
	}
	if p := jobProgress(jobSteps("en", job{Status: "done", Log: log}), "done"); p != 100 {
		t.Errorf("done is 100: %d", p)
	}
}

func TestJobPageShowsStepsBannerAndFoldedLog(t *testing.T) {
	r, h, _ := newInstallRig(t)
	_ = h
	startInstall(r, r.csrf(), "mcserver", "My MC")
	waitJob(t, r, 1)
	page := r.do("GET", "/jobs/1", nil, nil).Body.String()
	for _, want := range []string{`class="steps"`, `class="step done"`, "<progress", "<details class=\"raw\"", "Server installed"} {
		if !strings.Contains(page, want) {
			t.Errorf("job page lacks %s", want)
		}
	}
}

func TestOwnerSeesTheUpdateBadgeOnlyWhenThereIsAnUpdate(t *testing.T) {
	r, _ := updRig(t, "v0.1.2", "v0.1.4")
	r.do("POST", "/updates/check", url.Values{"csrf": {r.csrf()}}, nil)
	body := r.do("GET", "/", nil, nil).Body.String()
	if !strings.Contains(body, `class="upd-badge"`) || !strings.Contains(body, `href="/updates"`) {
		t.Error("the owner must see the update badge")
	}
	r.viewer()
	if strings.Contains(r.do("GET", "/", nil, nil).Body.String(), "upd-badge") {
		t.Error("only the owner sees the badge")
	}
	r2, _ := updRig(t, "v0.1.4", "v0.1.4")
	r2.do("POST", "/updates/check", url.Values{"csrf": {r2.csrf()}}, nil)
	if strings.Contains(r2.do("GET", "/", nil, nil).Body.String(), "upd-badge") {
		t.Error("no update, no badge")
	}
}

func storeServers(n int) []store.Server {
	out := make([]store.Server, n)
	for i := range out {
		out[i] = store.Server{ID: int64(i + 1), Name: "s", Game: "mcserver", Mode: "managed"}
	}
	return out
}
