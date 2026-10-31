package entitlement

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/licsrv"
	"github.com/stokehold-gsm/stokehold/pkg/license"
)

type mem map[string]string

func (m mem) Setting(k string) (string, bool) { v, ok := m[k]; return v, ok }
func (m mem) SetSetting(k, v string) error    { m[k] = v; return nil }

type rig struct {
	t        *testing.T
	store    *licsrv.Store
	srv      *httptest.Server
	hits     atomic.Int32
	rootPub  ed25519.PublicKey
	clock    time.Time
	settings mem
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := licsrv.Open(filepath.Join(t.TempDir(), "lic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rootPub, rootPriv, _ := ed25519.GenerateKey(rand.Reader)
	onPub, onPriv, _ := ed25519.GenerateKey(rand.Reader)
	r := &rig{t: t, store: st, rootPub: rootPub, clock: time.Now(), settings: mem{}}
	cert := license.IssueCert("root1", rootPriv, "online-1", onPub, r.clock.Add(90*24*time.Hour))
	ls := licsrv.NewServer(st, onPriv, cert)
	ls.Now = func() time.Time { return r.clock }
	h := ls.Handler()
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rig) manager(settings mem) *Manager {
	return &Manager{
		S:   settings,
		V:   &license.Verifier{Roots: map[string]ed25519.PublicKey{"root1": r.rootPub}},
		URL: r.srv.URL,
		Now: func() time.Time { return r.clock },
	}
}

func (r *rig) issue(order string, servers int, days int) string {
	r.t.Helper()
	key, err := r.store.Issue("owner", order, "pro", servers, r.clock.Add(time.Duration(days)*24*time.Hour), "")
	if err != nil {
		r.t.Fatal(err)
	}
	return key
}

func (r *rig) advance(d time.Duration) { r.clock = r.clock.Add(d) }

var ctx = context.Background()

func TestFreeByDefault(t *testing.T) {
	r := newRig(t)
	m := r.manager(mem{})
	if m.Limit() != FreeServers || m.Status().Plan != "" {
		t.Errorf("fresh panel: %+v", m.Status())
	}
}

func TestActivateGivesProLimit(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	if err := m.Activate(ctx, r.issue("FP-1", 10, 30)); err != nil {
		t.Fatal(err)
	}
	st := m.Status()
	if st.Plan != "pro" || st.MaxServers != 10 || st.State != license.Active || !st.HasKey {
		t.Errorf("%+v", st)
	}
	// a restart sees the same state
	if m2 := r.manager(r.settings); m2.Limit() != 10 {
		t.Errorf("after restart: %d", m2.Limit())
	}
}

func TestTypoNeverLeavesThePanel(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	key := r.issue("FP-2", 10, 30)
	typo := []byte(key)
	typo[6] ^= 1
	if err := m.Activate(ctx, string(typo)); !errors.Is(err, ErrBadKey) {
		t.Errorf("typo: %v", err)
	}
	if r.hits.Load() != 0 {
		t.Error("a typo caused a network request")
	}
}

func TestNotConfiguredStaysFree(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	m.URL = ""
	if err := m.Activate(ctx, r.issue("FP-3", 10, 30)); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("%v", err)
	}
	m2 := r.manager(r.settings)
	m2.V.Roots = nil
	if err := m2.Activate(ctx, r.issue("FP-4", 10, 30)); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no trusted roots: %v", err)
	}
	if m.Limit() != FreeServers {
		t.Error("limit changed without a licence")
	}
}

func TestUnknownRevokedAndBoundKeys(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	other, _ := licsrv.NewKey()
	if err := m.Activate(ctx, other); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown key: %v", err)
	}
	if _, ok := r.settings.Setting(setKey); ok {
		t.Error("a refused key was stored")
	}
	key := r.issue("FP-5", 10, 30)
	if err := m.Activate(ctx, key); err != nil {
		t.Fatal(err)
	}
	second := r.manager(mem{}) // another panel, own instance key
	r.advance(time.Minute)
	if err := second.Activate(ctx, key); !errors.Is(err, ErrBound) {
		t.Errorf("second panel: %v", err)
	}
	if second.Limit() != FreeServers {
		t.Error("a refused activation gave Pro anyway")
	}
	r.store.Revoke("owner", "FP-5")
	r.advance(time.Minute)
	if err := m.Renew(ctx); !errors.Is(err, ErrRevoked) {
		t.Errorf("revoked: %v", err)
	}
}

func TestServerOutageKeepsWorkingThenGraceThenFree(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	if err := m.Activate(ctx, r.issue("FP-6", 10, 365)); err != nil {
		t.Fatal(err)
	}
	r.srv.Close() // the licence server goes away
	r.advance(24 * time.Hour)
	if err := m.Renew(ctx); !errors.Is(err, ErrUnreachable) {
		t.Errorf("outage: %v", err)
	}
	if m.Limit() != 10 {
		t.Errorf("a failed renewal must keep the token: %d", m.Limit())
	}
	r.advance(7 * 24 * time.Hour) // token (7 days) has now expired, grace begins
	if st := m.Status(); st.State != license.InGrace || st.MaxServers != 10 {
		t.Errorf("grace: %+v", st)
	}
	r.advance(31 * 24 * time.Hour)
	if st := m.Status(); st.MaxServers != FreeServers || st.Plan != "" {
		t.Errorf("after grace: %+v", st)
	}
	if m.Status().LastError == "" {
		t.Error("the outage should be visible on the licence page")
	}
}

func TestExpiredLicenseIsNotRenewed(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	if err := m.Activate(ctx, r.issue("FP-7", 10, 3)); err != nil {
		t.Fatal(err)
	}
	r.advance(4 * 24 * time.Hour)
	if err := m.Renew(ctx); !errors.Is(err, ErrExpired) {
		t.Errorf("%v", err)
	}
	// the token was capped at the licence end, so we are in grace, still Pro
	if st := m.Status(); st.State != license.InGrace {
		t.Errorf("%+v", st)
	}
}

func TestTamperedTokenFallsBackToFree(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	if err := m.Activate(ctx, r.issue("FP-8", 10, 30)); err != nil {
		t.Fatal(err)
	}
	tok := r.settings[setToken]
	for name, bad := range map[string]string{
		"flipped": strings.Replace(tok, tok[10:12], "AA", 1) + "",
		"junk":    "not-a-token",
		"empty":   "",
	} {
		r.settings[setToken] = bad
		if m.Limit() != FreeServers {
			t.Errorf("%s: limit %d", name, m.Limit())
		}
	}
}

func TestClockRollbackDoesNotRevivePro(t *testing.T) {
	r := newRig(t)
	m := r.manager(r.settings)
	if err := m.Activate(ctx, r.issue("FP-9", 10, 365)); err != nil {
		t.Fatal(err)
	}
	start := r.clock
	r.srv.Close()
	r.advance(60 * 24 * time.Hour)
	if m.Limit() != FreeServers {
		t.Fatal("expected Free after token and grace ended")
	}
	r.clock = start.Add(24 * time.Hour) // the user winds the clock back
	m2 := r.manager(r.settings)         // even across a restart
	if m2.Limit() != FreeServers {
		t.Error("winding the clock back revived an expired token")
	}
}

func TestTokenFromAnUntrustedRootIsNotStored(t *testing.T) {
	r := newRig(t)
	m := r.manager(mem{})
	otherRoot, _, _ := ed25519.GenerateKey(rand.Reader)
	m.V.Roots = map[string]ed25519.PublicKey{"root1": otherRoot}
	err := m.Activate(ctx, r.issue("FP-10", 10, 30))
	if err == nil || !strings.Contains(err.Error(), "does not trust") {
		t.Errorf("%v", err)
	}
	if m.Limit() != FreeServers {
		t.Error("an untrusted token was accepted")
	}
}

func TestEmbeddedRootsLoaderIgnoresNonKeys(t *testing.T) {
	// the repository ships a README and no keys until the owner adds one
	for id := range EmbeddedRoots() {
		if id == "" || strings.Contains(id, ".") {
			t.Errorf("odd root id %q", id)
		}
	}
}
