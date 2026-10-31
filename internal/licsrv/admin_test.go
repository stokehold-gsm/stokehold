package licsrv

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/auth"
)

type arig struct {
	t   *testing.T
	st  *Store
	ui  *AdminUI
	h   http.Handler
	jar []*http.Cookie
	now time.Time
}

const apass = "a long enough admin pass"

func newARig(t *testing.T) *arig {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "lic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &arig{t: t, st: st, now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	st.now = func() time.Time { return r.now }
	r.ui = NewAdminUI(st, false, false)
	r.ui.Now = func() time.Time { return r.now }
	r.h = r.ui.Handler()
	return r
}

// admin creates an admin with two-factor already on and returns its TOTP secret.
func (r *arig) admin(name, role string) string {
	r.t.Helper()
	if err := r.st.AdminCreate(name, apass, role); err != nil {
		r.t.Fatal(err)
	}
	a, _ := r.st.AdminLogin(name, apass)
	secret := auth.NewTOTPSecret()
	r.st.AdminSetTOTP(a.ID, secret, true)
	return secret
}

func (r *arig) do(method, path string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	r.t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "203.0.113.5:1"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range r.jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == adminCookie {
			r.jar = nil
			if c.MaxAge >= 0 {
				r.jar = append(r.jar, c)
			}
		}
	}
	return rec
}

func (r *arig) login(name, secret string) *httptest.ResponseRecorder {
	code := ""
	if secret != "" {
		code, _ = auth.Code(secret, r.now)
	}
	return r.do("POST", "/login", url.Values{"name": {name}, "password": {apass}, "code": {code}}, nil)
}

var acsrf = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (r *arig) csrf() string {
	r.t.Helper()
	m := acsrf.FindStringSubmatch(r.do("GET", "/", nil, nil).Body.String())
	if m == nil {
		r.t.Fatal("no csrf on the page")
	}
	return m[1]
}

func (r *arig) signIn(name, secret string) string {
	r.t.Helper()
	r.jar = nil
	if rec := r.login(name, secret); rec.Code != 303 {
		r.t.Fatalf("sign in %s: %d", name, rec.Code)
	}
	r.now = r.now.Add(31 * time.Second) // the next login needs a fresh TOTP step
	return r.csrf()
}

func issueForm(tok, order string) url.Values {
	return url.Values{"csrf": {tok}, "order": {order}, "servers": {"10"}, "days": {"365"}, "contact": {"buyer"}}
}

func TestAnonymousIsSentToLogin(t *testing.T) {
	r := newARig(t)
	for _, p := range []string{"/", "/issue", "/audit", "/security"} {
		if rec := r.do("GET", p, nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/login" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := r.do("POST", "/issue", issueForm("x", "A"), nil); rec.Code != 303 {
		t.Errorf("anonymous issue: %d", rec.Code)
	}
}

func TestWithoutTwoFactorEverythingElseIsClosed(t *testing.T) {
	r := newARig(t)
	r.st.AdminCreate("helper", apass, RoleIssuer)
	if rec := r.login("helper", ""); rec.Code != 303 {
		t.Fatalf("login: %d", rec.Code)
	}
	for _, p := range []string{"/", "/issue"} {
		if rec := r.do("GET", p, nil, nil); rec.Header().Get("Location") != "/security" {
			t.Errorf("%s should lead to the 2FA page, got %q", p, rec.Header().Get("Location"))
		}
	}
	if rec := r.do("GET", "/security", nil, nil); rec.Code != 200 {
		t.Errorf("security page: %d", rec.Code)
	}
	// turn it on through the UI
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(r.do("GET", "/security", nil, nil).Body.String())
	r.do("POST", "/security/start", url.Values{"csrf": {m[1]}}, nil)
	sec := regexp.MustCompile(`<p class="k">([A-Z2-7]{32})</p>`).FindStringSubmatch(r.do("GET", "/security", nil, nil).Body.String())
	if sec == nil {
		t.Fatal("secret not shown")
	}
	code, _ := auth.Code(sec[1], r.now)
	if rec := r.do("POST", "/security/enable", url.Values{"csrf": {m[1]}, "code": {code}}, nil); rec.Code != 303 {
		t.Fatalf("enable: %d", rec.Code)
	}
	if rec := r.do("GET", "/issue", nil, nil); rec.Code != 200 {
		t.Errorf("after enabling 2FA: %d", rec.Code)
	}
}

func TestLoginNeedsTheCodeAndNotTwice(t *testing.T) {
	r := newARig(t)
	secret := r.admin("owner", RoleOwner)
	if rec := r.login("owner", ""); rec.Code != 401 {
		t.Errorf("no code: %d", rec.Code)
	}
	if rec := r.login("owner", secret); rec.Code != 303 {
		t.Fatalf("with code: %d", rec.Code)
	}
	r.jar = nil
	if rec := r.login("owner", secret); rec.Code != 401 {
		t.Errorf("the same code twice: %d", rec.Code)
	}
}

func TestIssuerIssuesOnceAndTheKeyIsNotStored(t *testing.T) {
	r := newARig(t)
	secret := r.admin("helper", RoleIssuer)
	tok := r.signIn("helper", secret)
	rec := r.do("POST", "/issue", issueForm(tok, "FP-100"), nil)
	if rec.Code != 200 {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	m := regexp.MustCompile(`SKH-[A-Z0-9-]+`).FindString(rec.Body.String())
	if m == "" {
		t.Fatal("the key was not shown")
	}
	norm, ok := ParseKey(m)
	if !ok {
		t.Fatal("the shown key does not parse")
	}
	if l, err := r.st.ByKey(norm); err != nil || l.CreatedBy != "helper" || l.OrderRef != "FP-100" {
		t.Errorf("stored licence: %+v %v", l, err)
	}
	var found int
	r.st.db.QueryRow(`select count(*) from licenses where key_hash=?`, norm).Scan(&found)
	if found != 0 {
		t.Error("the plain key is in the database")
	}
	if rec := r.do("POST", "/issue", issueForm(tok, "FP-100"), nil); rec.Code != 409 {
		t.Errorf("second key for one order: %d", rec.Code)
	}
	for _, bad := range []url.Values{
		{"csrf": {tok}, "order": {""}, "servers": {"10"}, "days": {"30"}},
		{"csrf": {tok}, "order": {"X"}, "servers": {"0"}, "days": {"30"}},
		{"csrf": {tok}, "order": {"X"}, "servers": {"10"}, "days": {"99999"}},
		{"csrf": {tok}, "order": {"X"}, "servers": {"abc"}, "days": {"30"}},
	} {
		if rec := r.do("POST", "/issue", bad, nil); rec.Code != 400 {
			t.Errorf("%v: %d", bad, rec.Code)
		}
	}
}

func TestIssuerDailyLimitAndOwnerExempt(t *testing.T) {
	r := newARig(t)
	hs, os := r.admin("helper", RoleIssuer), r.admin("owner", RoleOwner)
	tok := r.signIn("helper", hs)
	for i := 0; i < IssuerDailyMax; i++ {
		if rec := r.do("POST", "/issue", issueForm(tok, "H-"+suffix(i)), nil); rec.Code != 200 {
			t.Fatalf("issue %d: %d", i, rec.Code)
		}
	}
	if rec := r.do("POST", "/issue", issueForm(tok, "H-extra"), nil); rec.Code != 429 {
		t.Errorf("past the daily limit: %d", rec.Code)
	}
	r.now = r.now.Add(25 * time.Hour) // a day later the budget is back
	tok = r.signIn("helper", hs)
	if rec := r.do("POST", "/issue", issueForm(tok, "H-next-day"), nil); rec.Code != 200 {
		t.Errorf("next day: %d", rec.Code)
	}
	tok = r.signIn("owner", os)
	for i := 0; i < IssuerDailyMax+2; i++ {
		if rec := r.do("POST", "/issue", issueForm(tok, "O-"+suffix(i)), nil); rec.Code != 200 {
			t.Fatalf("owner issue %d: %d", i, rec.Code)
		}
	}
}

func suffix(i int) string { return string(rune('a'+i/26)) + string(rune('a'+i%26)) }

func TestOnlyTheOwnerCanRevokeExtendResetOrReadTheAudit(t *testing.T) {
	r := newARig(t)
	hs, os := r.admin("helper", RoleIssuer), r.admin("owner", RoleOwner)
	r.st.Issue("owner", "FP-7", "pro", 5, r.now.Add(10*24*time.Hour), "")

	tok := r.signIn("helper", hs)
	list := r.do("GET", "/", nil, nil).Body.String()
	if !strings.Contains(list, "FP-7") || strings.Contains(list, "/license/revoke") {
		t.Error("an issuer sees the list but no owner actions")
	}
	for _, act := range []string{"revoke", "extend", "reset"} {
		if rec := r.do("POST", "/license/"+act, url.Values{"csrf": {tok}, "order": {"FP-7"}, "days": {"30"}}, nil); rec.Code != 403 {
			t.Errorf("issuer %s: %d", act, rec.Code)
		}
	}
	if rec := r.do("GET", "/audit", nil, nil); rec.Code != 403 {
		t.Errorf("issuer reading the audit: %d", rec.Code)
	}
	if l, _ := r.st.ByOrder("FP-7"); l.Status != StatusActive {
		t.Fatal("an issuer changed a licence")
	}

	tok = r.signIn("owner", os)
	if rec := r.do("POST", "/license/extend", url.Values{"csrf": {tok}, "order": {"FP-7"}, "days": {"30"}}, nil); rec.Code != 303 {
		t.Errorf("owner extend: %d", rec.Code)
	}
	if l, _ := r.st.ByOrder("FP-7"); time.Unix(l.ExpiresAt, 0).Before(r.now.Add(39 * 24 * time.Hour)) {
		t.Errorf("not extended: %v", time.Unix(l.ExpiresAt, 0))
	}
	if rec := r.do("POST", "/license/revoke", url.Values{"csrf": {tok}, "order": {"FP-7"}}, nil); rec.Code != 303 {
		t.Errorf("owner revoke: %d", rec.Code)
	}
	if l, _ := r.st.ByOrder("FP-7"); l.Status != StatusRevoked {
		t.Error("not revoked")
	}
	if rec := r.do("POST", "/license/revoke", url.Values{"csrf": {tok}, "order": {"NOPE"}}, nil); rec.Code != 404 {
		t.Errorf("unknown order: %d", rec.Code)
	}
	if rec := r.do("POST", "/license/explode", url.Values{"csrf": {tok}, "order": {"FP-7"}}, nil); rec.Code != 404 {
		t.Errorf("unknown action: %d", rec.Code)
	}
	body := r.do("GET", "/audit", nil, nil).Body.String()
	if !strings.Contains(body, "revoke") || !strings.Contains(body, "Цепочка журнала цела") {
		t.Error("audit page should list actions and the chain state")
	}
	r.st.db.Exec(`update audit set detail='order=hidden' where id=1`)
	if !strings.Contains(r.do("GET", "/audit", nil, nil).Body.String(), "нарушена") {
		t.Error("a tampered audit must be flagged on the page")
	}
}

func TestCSRFAndCrossSiteArePrevented(t *testing.T) {
	r := newARig(t)
	secret := r.admin("helper", RoleIssuer)
	tok := r.signIn("helper", secret)
	if rec := r.do("POST", "/issue", issueForm("wrong", "FP-1"), nil); rec.Code != 403 {
		t.Errorf("bad csrf: %d", rec.Code)
	}
	if rec := r.do("POST", "/issue", issueForm(tok, "FP-1"), map[string]string{"Origin": "https://evil.example"}); rec.Code != 403 {
		t.Errorf("foreign origin: %d", rec.Code)
	}
	if rec := r.do("POST", "/issue", url.Values{"order": {"FP-1"}, "servers": {"1"}, "days": {"1"}}, nil); rec.Code != 403 {
		t.Errorf("no csrf: %d", rec.Code)
	}
}

func TestLockoutAndRateLimit(t *testing.T) {
	r := newARig(t)
	secret := r.admin("owner", RoleOwner)
	for i := 0; i < maxFailures; i++ {
		r.do("POST", "/login", url.Values{"name": {"owner"}, "password": {"wrong password!"}}, nil)
	}
	if rec := r.login("owner", secret); rec.Code != 401 || !strings.Contains(rec.Body.String(), "заблокирована") {
		t.Errorf("locked account: %d", rec.Code)
	}
	r2 := newARig(t)
	var last int
	for i := 0; i < 12; i++ {
		last = r2.do("POST", "/login", url.Values{"name": {"x"}, "password": {"y"}}, nil).Code
	}
	if last != 429 {
		t.Errorf("12th attempt: %d", last)
	}
}

func TestHeadersAndNoPublicSurface(t *testing.T) {
	r := newARig(t)
	h := r.do("GET", "/login", nil, nil).Header()
	if h.Get("X-Frame-Options") != "DENY" || h.Get("Cache-Control") != "no-store" || !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("headers: %v", h)
	}
	if strings.Contains(h.Get("Content-Security-Policy"), "script") {
		t.Error("no script source should be allowed")
	}
	if rec := r.do("GET", "/v1/token", nil, nil); rec.Code != 404 && rec.Code != 405 {
		t.Errorf("the admin UI must not serve the token API: %d", rec.Code)
	}
}
