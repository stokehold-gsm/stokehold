package web

import (
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/store"
)

// Owner settings live in the settings table next to the webhook and the like.
const (
	setRequire2FA   = "require_2fa"
	setSessionHours = "session_hours"
	setDefaultLang  = "default_lang"

	defaultSessionHours = 12
	maxSessionHours     = 168
)

// defaultLang is the owner's choice for visitors who have no cookie yet. It is
// a process-wide value because lang() runs far from any Server.
var defaultLang atomic.Value

func (s *Server) loadSettings() {
	v, _ := s.Store.Setting(setDefaultLang)
	defaultLang.Store(v)
}

func (s *Server) require2FA() bool {
	v, _ := s.Store.Setting(setRequire2FA)
	return v == "1"
}

func (s *Server) sessionHours() int {
	v, _ := s.Store.Setting(setSessionHours)
	if h, err := strconv.Atoi(v); err == nil && h >= 1 && h <= maxSessionHours {
		return h
	}
	return defaultSessionHours
}

func (s *Server) sessionTTL() time.Duration { return time.Duration(s.sessionHours()) * time.Hour }

// without2FAOK lists what a user who must set up two-factor may still open.
func without2FAOK(path string) bool {
	return path == "/security" || strings.HasPrefix(path, "/security/") || path == "/logout" || path == "/theme"
}

func (s *Server) fillSettings(p *page) {
	p.SetRequire2FA = s.require2FA()
	p.SetSessionHours = s.sessionHours()
	v, _ := s.Store.Setting(setDefaultLang)
	p.SetDefaultLang = v
	p.PanelVersion = s.Version
	p.DataDir = s.DataDir
	p.HTTPSOn = s.TLSAddr != ""
	s.fillLicense(p)
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	if r.URL.Query().Get("ok") != "" {
		p.Info = tr(p.Lang, "set_saved")
	}
	s.fillSettings(p)
	s.render(w, "settings", p, http.StatusOK)
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	hours, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("session_hours")))
	lang := r.PostFormValue("default_lang")
	if err != nil || hours < 1 || hours > maxSessionHours || (lang != "" && lang != "ru" && lang != "en") {
		s.fillSettings(p)
		p.Error = tr(p.Lang, "set_bad")
		s.render(w, "settings", p, http.StatusBadRequest)
		return
	}
	on := "0"
	if r.PostFormValue("require_2fa") == "1" {
		on = "1"
	}
	if prev := s.require2FA(); (on == "1") != prev {
		s.Store.Audit(p.User.ID, "settings.require-2fa", map[string]string{"1": "on", "0": "off"}[on])
	}
	s.Store.SetSetting(setRequire2FA, on)
	s.Store.SetSetting(setSessionHours, strconv.Itoa(hours))
	s.Store.SetSetting(setDefaultLang, lang)
	defaultLang.Store(lang)
	s.Store.Audit(p.User.ID, "settings.save", "session_hours="+strconv.Itoa(hours)+" lang="+lang)
	http.Redirect(w, r, "/settings?ok=1", http.StatusSeeOther)
}
