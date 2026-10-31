// Package web is the panel's HTTP UI.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/catalog"
	"github.com/stokehold-gsm/stokehold/internal/entitlement"
	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/metrics"
	"github.com/stokehold-gsm/stokehold/internal/selfupdate"
	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/pkg/auth"
	"github.com/stokehold-gsm/stokehold/pkg/ratelimit"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

const cookieName = "sh_session"

type Server struct {
	Store       *store.Store
	Secure      bool   // mark cookies Secure; set when served over HTTPS
	TrustProxy  bool   // take the client address from the last X-Forwarded-For entry
	SetupToken  string // required by /setup while there are no users
	Helper      HelperFunc
	Entitlement Entitlements // nil means Free only
	Distro      string       // e.g. ubuntu-22.04, selects the LinuxGSM package list

	DataDir string // shown on the settings page
	TLSAddr string // the direct HTTPS listener, empty when off

	Metrics    *metrics.Collector                                                     // nil: no monitoring
	HookClient *http.Client                                                           // nil: a default client; tests replace it
	Version    string                                                                 // this build, for the updates page and the settings page
	Releases   func(ctx context.Context, channel string) (*selfupdate.Release, error) // nil: ask GitHub; tests replace it
	upd        updState
	dcache     detailsCache
	status     statusCache

	jobs jobTracker
	Now  func() time.Time

	tmpl    *template.Template
	limiter *ratelimit.Limiter
}

type page struct {
	Lang    string
	User    *store.User
	CSRF    string
	Error   string
	Servers []store.Server
	Secret  string
	URI     string

	AddUser    string
	Found      []string
	Result     string
	ServerName string
	Action     string
	Actions    []string
	CanControl bool

	Lic              entitlement.Status
	LicExpires       string
	IsGrace          bool
	Count            int
	Info             string
	CanChangeLicense bool
	CurPlan          string

	Theme, ThemeNext string // set by render from the cookie
	Rows             []srvRow
	Steps            []jobStep
	Pct              int
	Elapsed          string
	JobError         string
	UpdTag           string // a newer release, shown as a badge in the owner's header
	Need2FA          bool   // the owner requires two-factor and this user has none yet

	SetRequire2FA   bool
	SetSessionHours int
	SetDefaultLang  string
	PanelVersion    string
	DataDir         string
	HTTPSOn         bool
	PlanRows        []planRowView

	UserRows    []store.UserRow
	Sched       []schedRow
	Games       []catalog.Game
	Files       []string
	FM          *fmView
	Upd         *updView
	Srv         *srvPage
	FilePath    string
	FileContent string
	Refresh     bool
	ServerID    int64
	NewEmail    string
	NewPassword string

	proPage
	SetupCode string // prefilled from the link the installer prints
	NodeNames map[int64]string
	NodeID    int64
	Mon       monView
	Statuses  map[int64]ServerStatus
}

func New(st *store.Store, secure, trustProxy bool, setupToken string) *Server {
	s := &Server{Distro: helper.CurrentDistro(), Store: st, Secure: secure, TrustProxy: trustProxy, SetupToken: setupToken, Now: time.Now, limiter: ratelimit.New(10, time.Minute)}
	s.tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"t":   func(p page, key string) string { return tr(p.Lang, key) },
		"pro": func() bool { return proBuild },
	}).ParseFS(templatesFS, "templates/*.html"))
	s.loadSettings()
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", s.auth(s.dashboard))
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /setup", s.setupForm)
	mux.HandleFunc("POST /setup", s.setup)
	mux.HandleFunc("POST /logout", s.auth(s.logout))
	mux.HandleFunc("GET /servers/new", s.auth(s.newServerForm))
	mux.HandleFunc("POST /servers/new", s.auth(s.newServer))
	mux.HandleFunc("GET /jobs/{id}", s.auth(s.jobPage))
	mux.HandleFunc("GET /servers/add", s.auth(s.addForm))
	mux.HandleFunc("POST /servers/find", s.auth(s.find))
	mux.HandleFunc("POST /servers/add", s.auth(s.addServer))
	mux.HandleFunc("POST /servers/{id}/run", s.auth(s.runAction))
	mux.HandleFunc("GET /servers/{id}/schedules", s.auth(s.schedulesPage))
	mux.HandleFunc("POST /servers/{id}/schedules", s.auth(s.scheduleAdd))
	mux.HandleFunc("POST /schedules/{id}/delete", s.auth(s.scheduleDelete))
	mux.HandleFunc("GET /servers/{id}/files", s.auth(s.filesPage))
	mux.HandleFunc("GET /servers/{id}/edit", s.auth(s.editPage))
	mux.HandleFunc("POST /servers/{id}/edit", s.auth(s.editSave))
	mux.HandleFunc("GET /users", s.auth(s.usersPage))
	mux.HandleFunc("POST /users/create", s.auth(s.userCreate))
	mux.HandleFunc("POST /users/{id}/{action}", s.auth(s.userAction))
	mux.HandleFunc("POST /security/password", s.auth(s.changePassword))
	mux.HandleFunc("GET /theme", s.themeSet)
	mux.HandleFunc("GET /settings", s.auth(s.settingsPage))
	mux.HandleFunc("POST /settings", s.auth(s.settingsSave))
	mux.HandleFunc("GET /license", s.auth(s.licensePage))
	mux.HandleFunc("POST /license/activate", s.auth(s.licenseActivate))
	mux.HandleFunc("POST /license/renew", s.auth(s.licenseRenew))
	mux.HandleFunc("GET /security", s.auth(s.security))
	mux.HandleFunc("POST /security/2fa/start", s.auth(s.twofaStart))
	mux.HandleFunc("POST /security/2fa/enable", s.auth(s.twofaEnable))
	mux.HandleFunc("POST /security/2fa/disable", s.auth(s.twofaDisable))
	mux.HandleFunc("GET /monitor", s.auth(s.monitorPage))
	s.fmRoutes(mux)
	s.updRoutes(mux)
	s.srvRoutes(mux)
	s.proRoutes(mux)
	return s.protect(mux)
}

func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin") // no-referrer makes browsers send "Origin: null" on form posts
		h.Set("X-Frame-Options", "DENY")
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(&themeWriter{ResponseWriter: w, theme: themeOf(r)}, r)
	})
}

// sameOrigin checks the Origin header when a browser sends one, falls back to
// Sec-Fetch-Site, and lets plain non-browser clients through (they hold no
// cookies of ours, and session posts also need the CSRF token).
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != "null" { // "null" is what a form post carries under a strict referrer policy: fall through to Sec-Fetch-Site
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	if f := r.Header.Get("Sec-Fetch-Site"); f != "" {
		return f == "same-origin" || f == "none"
	}
	return true
}

type handler func(w http.ResponseWriter, r *http.Request, p *page)

// auth resolves the session, rejects bad CSRF tokens on posts, and passes a
// prepared page to the handler.
func (s *Server) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, csrf, err := s.Store.SessionUser(c.Value)
		if err != nil {
			s.clearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(csrf)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		need2FA := s.require2FA() && !u.TOTPEnabled
		if need2FA && !without2FAOK(r.URL.Path) {
			http.Redirect(w, r, "/security", http.StatusSeeOther)
			return
		}
		pg := &page{Lang: lang(w, r), User: u, CSRF: csrf, Need2FA: need2FA}
		if u.Role == store.RoleOwner {
			pg.UpdTag = s.UpdateAvailable()
		}
		h(w, r, pg)
	}
}

func (s *Server) render(w http.ResponseWriter, name string, p *page, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	pp := *p
	pp.Theme = themeFor(w)
	pp.ThemeNext = themeNext(pp.Theme)
	if err := s.tmpl.ExecuteTemplate(w, name, pp); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *Server) setCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(ttl.Seconds())})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy && r.TLS == nil { // a direct TLS client could forge the header
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, p *page) {
	p.Servers, _ = s.Store.Servers()
	s.fillNodes(p)
	p.Statuses = s.Statuses()
	p.Rows = srvRows(p.Servers, p.Statuses, p.NodeNames)
	p.CanControl = canControl(p.User.Role)
	p.Actions = actionLabels
	s.fillLicense(p)
	s.render(w, "dashboard", p, http.StatusOK)
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.Store.CountUsers(); n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, "login", &page{Lang: lang(w, r)}, http.StatusOK)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	p := &page{Lang: lang(w, r)}
	if !s.limiter.Allow(s.clientIP(r), s.Now()) {
		p.Error = tr(p.Lang, "locked")
		s.render(w, "login", p, http.StatusTooManyRequests)
		return
	}
	email := strings.TrimSpace(r.PostFormValue("email"))
	u, err := s.Store.CheckLogin(email, r.PostFormValue("password"))
	if err == nil && u.TOTPEnabled {
		step, ok := auth.VerifyTOTP(u.TOTPSecret, r.PostFormValue("code"), s.Now())
		if !ok || !s.Store.UseTOTPStep(u.ID, step) {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		s.Store.Audit(0, "login.fail", email)
		p.Error = tr(p.Lang, "bad_login")
		if errors.Is(err, store.ErrLocked) {
			p.Error = tr(p.Lang, "locked")
		}
		s.render(w, "login", p, http.StatusUnauthorized)
		return
	}
	ttl := s.sessionTTL()
	token, _, err := s.Store.CreateSessionTTL(u.ID, ttl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, token, ttl)
	s.Store.Audit(u.ID, "login.ok", "")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, p *page) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Store.DeleteSession(c.Value)
	}
	s.Store.Audit(p.User.ID, "logout", "")
	s.clearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) setupForm(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.Store.CountUsers(); n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, "setup", &page{Lang: lang(w, r), SetupCode: r.URL.Query().Get("code")}, http.StatusOK)
}

// setup: first owner only, needs the code from the service log.
// users, and only with the code printed in the service log at startup.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	p := &page{Lang: lang(w, r)}
	if n, _ := s.Store.CountUsers(); n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.limiter.Allow(s.clientIP(r), s.Now()) {
		p.Error = tr(p.Lang, "locked")
		s.render(w, "setup", p, http.StatusTooManyRequests)
		return
	}
	if s.SetupToken == "" || subtle.ConstantTimeCompare([]byte(r.PostFormValue("token")), []byte(s.SetupToken)) != 1 {
		p.Error = tr(p.Lang, "bad_setup")
		s.render(w, "setup", p, http.StatusForbidden)
		return
	}
	id, err := s.Store.CreateUser(strings.TrimSpace(r.PostFormValue("email")), r.PostFormValue("password"), store.RoleOwner)
	if errors.Is(err, auth.ErrWeakPassword) {
		p.Error = tr(p.Lang, "weak_password")
		s.render(w, "setup", p, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.Audit(id, "setup.owner", "")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) security(w http.ResponseWriter, r *http.Request, p *page) {
	if r.URL.Query().Get("ok") != "" {
		p.Info = tr(p.Lang, "pw_done")
	}
	if !p.User.TOTPEnabled && p.User.TOTPSecret != "" {
		p.Secret = p.User.TOTPSecret
		p.URI = auth.TOTPURI(p.Secret, p.User.Email, "Stokehold GSM")
	}
	s.render(w, "security", p, http.StatusOK)
}

func (s *Server) twofaStart(w http.ResponseWriter, r *http.Request, p *page) {
	if !p.User.TOTPEnabled {
		s.Store.SetTOTP(p.User.ID, auth.NewTOTPSecret(), false)
	}
	http.Redirect(w, r, "/security", http.StatusSeeOther)
}

func (s *Server) twofaEnable(w http.ResponseWriter, r *http.Request, p *page) {
	if step, ok := auth.VerifyTOTP(p.User.TOTPSecret, r.PostFormValue("code"), s.Now()); ok && p.User.TOTPSecret != "" && s.Store.UseTOTPStep(p.User.ID, step) {
		s.Store.SetTOTP(p.User.ID, p.User.TOTPSecret, true)
		s.Store.Audit(p.User.ID, "2fa.enable", "")
		http.Redirect(w, r, "/security", http.StatusSeeOther)
		return
	}
	p.Error = tr(p.Lang, "twofa_bad")
	p.Secret = p.User.TOTPSecret
	p.URI = auth.TOTPURI(p.Secret, p.User.Email, "Stokehold GSM")
	s.render(w, "security", p, http.StatusBadRequest)
}

// twofaDisable asks for the password again: a stolen session alone must not be
// able to strip the second factor.
func (s *Server) twofaDisable(w http.ResponseWriter, r *http.Request, p *page) {
	if !auth.CheckPassword(r.PostFormValue("password"), p.User.PassHash) {
		p.Error = tr(p.Lang, "wrong_password")
		s.render(w, "security", p, http.StatusForbidden)
		return
	}
	s.Store.SetTOTP(p.User.ID, "", false)
	s.Store.Audit(p.User.ID, "2fa.disable", "")
	http.Redirect(w, r, "/security", http.StatusSeeOther)
}
