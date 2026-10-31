package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"stokehold/internal/entitlement"
	"stokehold/internal/helper"
	"stokehold/internal/license"
	"stokehold/internal/store"
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

func pro(n int) *fakeEnt {
	return &fakeEnt{st: entitlement.Status{Configured: true, HasKey: true, Plan: "pro", MaxServers: n, State: license.Active, ExpiresAt: 1_900_000_000}}
}

func TestFreeLimitBlocksTheSecondServerButNotTheFirst(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	f := &fakeHelper{ok: true, out: "fctrserver\nmcserver"}
	r.withHelper(f)
	tok := r.csrf()
	add := func(game, name string) int {
		return r.do("POST", "/servers/add", url.Values{"csrf": {tok}, "user": {"factorio"}, "game": {game}, "name": {name}}, nil).Code
	}
	if code := add("fctrserver", "one"); code != 303 {
		t.Fatalf("first server on Free: %d", code)
	}
	rec := r.do("POST", "/servers/add", url.Values{"csrf": {tok}, "user": {"factorio"}, "game": {"mcserver"}, "name": {"two"}}, nil)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "server limit") {
		t.Errorf("second server on Free: %d %s", rec.Code, rec.Body)
	}
	if list, _ := r.st.Servers(); len(list) != 1 {
		t.Errorf("servers stored: %d", len(list))
	}
	// the limit never touches what already runs
	f.out = "Status: STARTED"
	if rec := r.do("POST", idPath(1), url.Values{"csrf": {tok}, "cmd": {"restart"}}, nil); rec.Code != 200 {
		t.Errorf("existing server action under the limit: %d", rec.Code)
	}
	// Pro lifts it
	f.out = "fctrserver\nmcserver"
	r.s.Entitlement = pro(5)
	if code := add("mcserver", "two"); code != 303 {
		t.Errorf("second server on Pro: %d", code)
	}
}

func TestDashboardShowsTheCount(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	r.addServerRow()
	if body := r.do("GET", "/", nil, nil).Body.String(); !strings.Contains(body, "(1 / 1)") {
		t.Errorf("Free counter missing: %s", body)
	}
	r.s.Entitlement = pro(10)
	if body := r.do("GET", "/", nil, nil).Body.String(); !strings.Contains(body, "(1 / 10)") {
		t.Error("Pro counter missing")
	}
}

func TestOwnerActivatesAViewerCannot(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	f := &fakeEnt{st: entitlement.Status{Configured: true, MaxServers: 1}}
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
	f := &fakeEnt{st: entitlement.Status{Configured: true, MaxServers: 1}}
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
	f := pro(10)
	f.st.State = license.InGrace
	f.st.LastError = "licence server unreachable"
	r.s.Entitlement = f
	page := r.do("GET", "/license", nil, nil).Body.String()
	if !strings.Contains(page, "grace period") || !strings.Contains(page, "unreachable") {
		t.Errorf("grace state not shown: %s", page)
	}
}

var _ = helper.Request{}
