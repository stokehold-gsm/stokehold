package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/pkg/auth"
)

const (
	token = "setup-code-123"
	email = "owner@example.com"
	pass  = "a long enough password"
)

type rig struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	st  *store.Store
	jar []*http.Cookie
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(st, false, false, token)
	return &rig{t: t, s: s, h: s.Handler(), st: st}
}

func (r *rig) do(method, path string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	r.t.Helper()
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "203.0.113.7:5555"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range r.jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			r.jar = nil
			if c.MaxAge >= 0 {
				r.jar = append(r.jar, c)
			}
		}
	}
	return rec
}

func (r *rig) setup() {
	r.t.Helper()
	if rec := r.do("POST", "/setup", url.Values{"token": {token}, "email": {email}, "password": {pass}}, nil); rec.Code != 303 {
		r.t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
}

func (r *rig) login(code string) *httptest.ResponseRecorder {
	return r.do("POST", "/login", url.Values{"email": {email}, "password": {pass}, "code": {code}}, nil)
}

var csrfRE = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (r *rig) csrf() string {
	r.t.Helper()
	m := csrfRE.FindStringSubmatch(r.do("GET", "/", nil, nil).Body.String())
	if m == nil {
		r.t.Fatal("no csrf token on the page")
	}
	return m[1]
}

func TestFreshPanelLeadsToSetup(t *testing.T) {
	r := newRig(t)
	if rec := r.do("GET", "/", nil, nil); rec.Code != 303 || rec.Header().Get("Location") != "/login" {
		t.Errorf("/: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := r.do("GET", "/login", nil, nil); rec.Header().Get("Location") != "/setup" {
		t.Errorf("/login on an empty panel should go to /setup, got %q", rec.Header().Get("Location"))
	}
}

func TestSetupNeedsTheCodeAndWorksOnce(t *testing.T) {
	r := newRig(t)
	form := func(tok, pw string) url.Values { return url.Values{"token": {tok}, "email": {email}, "password": {pw}} }
	if rec := r.do("POST", "/setup", form("wrong", pass), nil); rec.Code != 403 {
		t.Errorf("wrong code: %d", rec.Code)
	}
	if rec := r.do("POST", "/setup", form(token, "short"), nil); rec.Code != 400 {
		t.Errorf("weak password: %d", rec.Code)
	}
	if n, _ := r.st.CountUsers(); n != 0 {
		t.Fatal("a failed setup created a user")
	}
	r.setup()
	if rec := r.do("POST", "/setup", form(token, pass), nil); rec.Header().Get("Location") != "/login" {
		t.Errorf("a second setup must be refused, got %d", rec.Code)
	}
	if n, _ := r.st.CountUsers(); n != 1 {
		t.Errorf("users: %d", n)
	}
}

func TestEmptySetupTokenDisablesSetup(t *testing.T) {
	r := newRig(t)
	r.s.SetupToken = ""
	rec := r.do("POST", "/setup", url.Values{"token": {""}, "email": {email}, "password": {pass}}, nil)
	if rec.Code != 403 {
		t.Errorf("an empty configured token must never match an empty submitted one: %d", rec.Code)
	}
}

func TestLoginAndSessionCookie(t *testing.T) {
	r := newRig(t)
	r.setup()
	if rec := r.do("POST", "/login", url.Values{"email": {email}, "password": {"nope nope nope"}}, nil); rec.Code != 401 {
		t.Errorf("bad password: %d", rec.Code)
	}
	r.s.Secure = true
	rec := r.login("")
	if rec.Code != 303 {
		t.Fatalf("login: %d", rec.Code)
	}
	var c *http.Cookie
	for _, x := range rec.Result().Cookies() {
		if x.Name == cookieName {
			c = x
		}
	}
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie flags wrong: %+v", c)
	}
	if body := r.do("GET", "/", nil, nil).Body.String(); !strings.Contains(body, email) {
		t.Error("dashboard does not show the signed-in user")
	}
}

func TestLogoutNeedsCSRFToken(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	if rec := r.do("POST", "/logout", url.Values{}, nil); rec.Code != 403 {
		t.Errorf("logout without a token: %d", rec.Code)
	}
	if rec := r.do("POST", "/logout", url.Values{"csrf": {"x" + tok}}, nil); rec.Code != 403 {
		t.Errorf("logout with a wrong token: %d", rec.Code)
	}
	if rec := r.do("POST", "/logout", url.Values{"csrf": {tok}}, nil); rec.Code != 303 {
		t.Errorf("logout with the right token: %d", rec.Code)
	}
	if rec := r.do("GET", "/", nil, nil); rec.Header().Get("Location") != "/login" {
		t.Error("session still valid after logout")
	}
}

func TestCrossSitePostsAreRefused(t *testing.T) {
	r := newRig(t)
	r.setup()
	form := url.Values{"email": {email}, "password": {pass}}
	if rec := r.do("POST", "/login", form, map[string]string{"Origin": "https://evil.example"}); rec.Code != 403 {
		t.Errorf("foreign Origin: %d", rec.Code)
	}
	if rec := r.do("POST", "/login", form, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != 403 {
		t.Errorf("cross-site fetch: %d", rec.Code)
	}
	if rec := r.do("POST", "/login", form, map[string]string{"Origin": "http://example.com"}); rec.Code != 303 {
		t.Errorf("same Origin should pass: %d", rec.Code) // httptest requests use Host example.com
	}
}

func TestSecurityHeaders(t *testing.T) {
	r := newRig(t)
	h := r.do("GET", "/login", nil, nil).Header()
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "same-origin", "Cache-Control": "no-store"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q", k, h.Get(k))
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") {
		t.Errorf("CSP: %q", csp)
	}
	if rec := r.do("GET", "/static/app.css", nil, nil); rec.Code != 200 {
		t.Errorf("static css: %d", rec.Code)
	}
}

func TestTwoFactorFlow(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	r.do("POST", "/security/2fa/start", url.Values{"csrf": {tok}}, nil)
	secret := regexp.MustCompile(`<code>([A-Z2-7]{32})</code>`).FindStringSubmatch(r.do("GET", "/security", nil, nil).Body.String())
	if secret == nil {
		t.Fatal("no secret shown after starting setup")
	}
	now := time.Now()
	if rec := r.do("POST", "/security/2fa/enable", url.Values{"csrf": {tok}, "code": {"000000"}}, nil); rec.Code != 400 {
		t.Errorf("wrong code must not enable: %d", rec.Code)
	}
	code, _ := auth.Code(secret[1], now)
	if rec := r.do("POST", "/security/2fa/enable", url.Values{"csrf": {tok}, "code": {code}}, nil); rec.Code != 303 {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	r.do("POST", "/logout", url.Values{"csrf": {tok}}, nil)

	if rec := r.login(""); rec.Code != 401 {
		t.Errorf("login without a code must fail once 2FA is on: %d", rec.Code)
	}
	if rec := r.login(code); rec.Code != 401 {
		t.Errorf("the code spent while enabling must not log in again: %d", rec.Code)
	}
	// next 30-second step, so a fresh code
	r.s.Now = func() time.Time { return now.Add(30 * time.Second) }
	fresh, _ := auth.Code(secret[1], now.Add(30*time.Second))
	if rec := r.login(fresh); rec.Code != 303 {
		t.Fatalf("login with a fresh code: %d", rec.Code)
	}
	r.do("POST", "/logout", url.Values{"csrf": {r.csrf()}}, nil)
	if rec := r.login(fresh); rec.Code != 401 {
		t.Errorf("a code must not work twice: %d", rec.Code)
	}
}

func TestDisablingTwoFactorNeedsPassword(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	r.do("POST", "/security/2fa/start", url.Values{"csrf": {tok}}, nil)
	u, _ := r.st.UserByID(1)
	code, _ := auth.Code(u.TOTPSecret, time.Now())
	r.do("POST", "/security/2fa/enable", url.Values{"csrf": {tok}, "code": {code}}, nil)
	if rec := r.do("POST", "/security/2fa/disable", url.Values{"csrf": {tok}, "password": {"wrong password"}}, nil); rec.Code != 403 {
		t.Errorf("disable with a wrong password: %d", rec.Code)
	}
	if u, _ := r.st.UserByID(1); !u.TOTPEnabled {
		t.Error("2FA was removed without the password")
	}
	if rec := r.do("POST", "/security/2fa/disable", url.Values{"csrf": {tok}, "password": {pass}}, nil); rec.Code != 303 {
		t.Errorf("disable with the password: %d", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	r := newRig(t)
	r.setup()
	var last int
	for i := 0; i < 12; i++ {
		last = r.do("POST", "/login", url.Values{"email": {email}, "password": {"wrong wrong wrong"}}, nil).Code
	}
	if last != 429 {
		t.Errorf("12th attempt from one address: %d, want 429", last)
	}
}

func TestLanguage(t *testing.T) {
	r := newRig(t)
	r.setup()
	if b := r.do("GET", "/login", nil, map[string]string{"Accept-Language": "ru-RU,ru;q=0.9"}).Body.String(); !strings.Contains(b, "Почта") {
		t.Error("Russian Accept-Language should give a Russian page")
	}
	if b := r.do("GET", "/login", nil, map[string]string{"Accept-Language": "de"}).Body.String(); !strings.Contains(b, "Email") {
		t.Error("unknown languages fall back to English")
	}
	rec := r.do("GET", "/login?lang=ru", nil, nil)
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "lang=ru") {
		t.Error("?lang= should be remembered in a cookie")
	}
}

func TestProxyAddressIsUsedOnlyWhenTrusted(t *testing.T) {
	r := newRig(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.9")
	if got := r.s.clientIP(req); got != "10.0.0.1" {
		t.Errorf("untrusted proxy header must be ignored, got %s", got)
	}
	r.s.TrustProxy = true
	if got := r.s.clientIP(req); got != "198.51.100.9" {
		t.Errorf("trusted proxy: take the last entry, got %s", got)
	}
}

func TestFormPostsFromABrowserWithANullOriginAreAccepted(t *testing.T) {
	r := newRig(t)
	form := url.Values{"token": {"wrong"}, "email": {"a@b.c"}, "password": {"longenoughpass"}}
	// the page is served with Referrer-Policy; browsers then send "Origin: null" on form posts
	if rec := r.do("POST", "/setup", form, map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"}); rec.Code == 403 && !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("a same-origin post with a null Origin must reach the handler: %d %s", rec.Code, rec.Body)
	}
	for name, hdr := range map[string]map[string]string{
		"cross-site":   {"Origin": "null", "Sec-Fetch-Site": "cross-site"},
		"other origin": {"Origin": "https://evil.example"},
	} {
		if rec := r.do("POST", "/setup", form, hdr); rec.Code != 403 || strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("%s: want the plain forbidden answer, got %d", name, rec.Code)
		}
	}
	if got := r.do("GET", "/login", nil, nil).Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("Referrer-Policy %q: no-referrer would make browsers send a null Origin", got)
	}
}

func TestTwoFactorSetupShowsAQRCode(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	r.do("POST", "/security/2fa/start", url.Values{"csrf": {tok}}, nil)
	if page := r.do("GET", "/security", nil, nil).Body.String(); !strings.Contains(page, `<svg class="qr"`) {
		t.Fatal("the 2FA setup page must show a QR code of the otpauth link")
	}
}
