package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stokehold-gsm/stokehold/internal/entitlement"
	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/pkg/license"
)

type fakeEnt struct {
	st        entitlement.Status
	activate  error
	activated []string
	renews    int
}

func (f *fakeEnt) Status() entitlement.Status { return f.st }
func (f *fakeEnt) Activate(_ context.Context, key string) error {
	f.activated = append(f.activated, key)
	return f.activate
}
func (f *fakeEnt) Renew(context.Context) error { f.renews++; return f.activate }

func pro() *fakeEnt {
	return &fakeEnt{st: entitlement.Status{Configured: true, HasKey: true, Plan: "pro", Features: []string{license.FeatureS3Backup, license.FeatureRollback}, State: license.Active, ExpiresAt: 1_900_000_000}}
}

func TestFreeHasNoServerLimit(t *testing.T) {
	r := newRig(t) // no licensing at all: the open core, Free
	r.setup()
	r.login("")
	f := &fakeHelper{ok: true, out: "fctrserver\nmcserver\nvhserver"}
	r.withHelper(f)
	tok := r.csrf()
	for i, game := range []string{"fctrserver", "mcserver", "vhserver"} {
		form := url.Values{"csrf": {tok}, "user": {"factorio"}, "game": {game}, "name": {"server-" + game}}
		if rec := r.do("POST", "/servers/add", form, nil); rec.Code != 303 {
			t.Fatalf("server %d on Free: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if list, _ := r.st.Servers(); len(list) != 3 {
		t.Errorf("servers stored: %d", len(list))
	}
}

func TestDashboardShowsTheServerCount(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	r.addServerRow()
	if body := r.do("GET", "/", nil, nil).Body.String(); !strings.Contains(body, "(1)") {
		t.Errorf("server counter missing: %s", body)
	}
}

func TestOwnerActivatesAViewerCannot(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	f := &fakeEnt{st: entitlement.Status{Configured: true}}
	r.s.Entitlement = f
	r.login("")
	tok := r.csrf()
	if rec := r.do("POST", "/license/activate", url.Values{"csrf": {tok}, "key": {"SKH-AAAA"}}, nil); rec.Code != 303 || len(f.activated) != 1 {
		t.Fatalf("owner activation: %d %v", rec.Code, f.activated)
	}
	if r.st.AuditCount("license.activate") != 1 {
		t.Error("activation not audited")
	}
	r.loginAs("viewer@example.com", pass)
	tok = r.csrf()
	f.activated = nil
	if rec := r.do("POST", "/license/activate", url.Values{"csrf": {tok}, "key": {"SKH-BBBB"}}, nil); rec.Code != 403 || len(f.activated) != 0 {
		t.Errorf("viewer activation: %d %v", rec.Code, f.activated)
	}
	page := r.do("GET", "/license", nil, nil).Body.String()
	if strings.Contains(page, `action="/license/activate"`) {
		t.Error("a viewer must not see the activation form")
	}
	if !strings.Contains(page, "What is sent") {
		t.Error("the data disclosure must be visible to everyone")
	}
}

func TestLicenseErrorsAreExplained(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	f := &fakeEnt{st: entitlement.Status{Configured: true}}
	r.s.Entitlement = f
	tok := r.csrf()
	for err, want := range map[error]string{
		entitlement.ErrBadKey:      "typed wrong",
		entitlement.ErrExpired:     "expired",
		entitlement.ErrRevoked:     "revoked",
		entitlement.ErrBound:       "another panel instance",
		entitlement.ErrUnreachable: "not reachable",
		entitlement.ErrInvalid:     "not found",
		errors.New("boom"):         "Could not activate",
	} {
		f.activate = err
		rec := r.do("POST", "/license/activate", url.Values{"csrf": {tok}, "key": {"x"}}, nil)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%v: %d, wanted %q in the page", err, rec.Code, want)
		}
	}
}

func TestNoLicensingMeansFreeAndAClearMessage(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	rec := r.do("POST", "/license/activate", url.Values{"csrf": {tok}, "key": {"x"}}, nil)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "not configured") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if page := r.do("GET", "/license", nil, nil).Body.String(); !strings.Contains(page, "Free") {
		t.Error("the licence page should show Free")
	}
}

func TestGraceIsExplained(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	f := pro()
	f.st.State = license.InGrace
	f.st.LastError = "licence server unreachable"
	r.s.Entitlement = f
	page := r.do("GET", "/license", nil, nil).Body.String()
	if !strings.Contains(page, "grace period") || !strings.Contains(page, "unreachable") {
		t.Errorf("grace state not shown: %s", page)
	}
}

var _ = helper.Request{}
