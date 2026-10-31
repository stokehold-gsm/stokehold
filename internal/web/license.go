package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/entitlement"
	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/pkg/license"
)

type Entitlements interface {
	Status() entitlement.Status
	Activate(ctx context.Context, key string) error
	Renew(ctx context.Context) error
}

func licenseMessage(lang string, err error) string {
	key := "lic_other"
	switch {
	case errors.Is(err, entitlement.ErrBadKey):
		key = "lic_bad_key"
	case errors.Is(err, entitlement.ErrNotConfigured):
		key = "lic_not_configured"
	case errors.Is(err, entitlement.ErrExpired):
		key = "lic_expired"
	case errors.Is(err, entitlement.ErrRevoked):
		key = "lic_revoked"
	case errors.Is(err, entitlement.ErrBound):
		key = "lic_bound"
	case errors.Is(err, entitlement.ErrUnreachable):
		key = "lic_unreachable"
	case errors.Is(err, entitlement.ErrInvalid):
		key = "lic_invalid"
	}
	return tr(lang, key)
}

func (s *Server) fillLicense(p *page) {
	if s.Entitlement == nil {
		p.Lic = entitlement.Status{}
	} else {
		p.Lic = s.Entitlement.Status()
	}
	if p.Lic.ExpiresAt > 0 {
		p.LicExpires = time.Unix(p.Lic.ExpiresAt, 0).UTC().Format("2006-01-02")
	}
	p.IsGrace = p.Lic.State == license.InGrace
	servers, _ := s.Store.Servers()
	p.Count = len(servers)
	p.CanChangeLicense = p.User.Role == store.RoleOwner
}

func (s *Server) licensePage(w http.ResponseWriter, r *http.Request, p *page) {
	s.fillLicense(p)
	if q := r.URL.Query().Get("ok"); q != "" {
		p.Info = tr(p.Lang, "lic_ok")
	}
	s.render(w, "license", p, http.StatusOK)
}

func (s *Server) licenseActivate(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	if s.Entitlement == nil {
		p.Error = tr(p.Lang, "lic_not_configured")
		s.fillLicense(p)
		s.render(w, "license", p, http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	key := strings.TrimSpace(r.PostFormValue("key"))
	if err := s.Entitlement.Activate(ctx, key); err != nil {
		s.Store.Audit(p.User.ID, "license.activate.fail", "")
		p.Error = licenseMessage(p.Lang, err)
		s.fillLicense(p)
		s.render(w, "license", p, http.StatusBadRequest)
		return
	}
	s.Store.Audit(p.User.ID, "license.activate", "")
	http.Redirect(w, r, "/license?ok=1", http.StatusSeeOther)
}

func (s *Server) licenseRenew(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner || s.Entitlement == nil {
		s.denied(w, p)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Entitlement.Renew(ctx); err != nil {
		p.Error = licenseMessage(p.Lang, err)
		s.fillLicense(p)
		s.render(w, "license", p, http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/license?ok=1", http.StatusSeeOther)
}
