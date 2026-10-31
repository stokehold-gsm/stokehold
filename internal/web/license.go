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

// planRow is one line of the comparison on the licence page. Rows with a
// Feature take their Pro and Hosting cells from the plans in pkg/license;
// Planned marks what a plan includes on paper but is not built yet.
type planRow struct {
	Key     string // i18n key of the label
	Feature string // licence feature behind the row, empty for the open core
	Planned bool
}

var planRows = []planRow{
	{Key: "pf_monitoring"},
	{Key: "pf_servers"},
	{Key: "pf_schedules"},
	{Key: "pf_editor"},
	{Key: "pf_filemgr"},
	{Key: "pf_gametools"},
	{Key: "pf_multiadmin"}, // users and roles are in the core: nothing gates them
	{Key: "pf_s3", Feature: license.FeatureS3Backup, Planned: true},
	{Key: "pf_rollback", Feature: license.FeatureRollback, Planned: true},
	{Key: "pf_api", Feature: license.FeatureAPI},
	{Key: "pf_mods", Feature: license.FeatureMods},
	{Key: "pf_discord", Feature: license.FeatureDiscord},
	{Key: "pf_status", Feature: license.FeatureStatusPage},
	{Key: "pf_nodes", Feature: license.FeatureMultiNode},
}

// planCols are the columns, in order; Free has no licence plan.
var planCols = []string{"free", "pro", "hosting"}

type planCell struct {
	Class, Text string
	Cur         bool // the column of the plan this panel is on
}

type planRowView struct {
	Label string
	Cells []planCell
}

func listHas(list []string, f string) bool {
	for _, x := range list {
		if x == f {
			return true
		}
	}
	return false
}

func planTable(lang, cur string) []planRowView {
	out := make([]planRowView, 0, len(planRows))
	for _, row := range planRows {
		v := planRowView{Label: tr(lang, row.Key)}
		for _, col := range planCols {
			c := planCell{Class: "y", Text: "✓", Cur: col == cur}
			if row.Feature != "" && !listHas(license.PlanFeatures(col), row.Feature) {
				c.Class, c.Text = "n", "—"
			} else if row.Feature != "" && row.Planned {
				c.Class, c.Text = "soon", tr(lang, "pf_soon")
			}
			v.Cells = append(v.Cells, c)
		}
		out = append(out, v)
	}
	return out
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
	p.CurPlan = "free"
	if license.KnownPlan(p.Lic.Plan) {
		p.CurPlan = p.Lic.Plan
	}
	p.PlanRows = planTable(p.Lang, p.CurPlan)
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
