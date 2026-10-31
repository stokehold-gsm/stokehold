package licsrv

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/license"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type rig struct {
	t        *testing.T
	st       *Store
	srv      *Server
	rootPub  ed25519.PublicKey
	instance ed25519.PrivateKey
	now      time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "lic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rootPub, rootPriv, _ := ed25519.GenerateKey(rand.Reader)
	onPub, onPriv, _ := ed25519.GenerateKey(rand.Reader)
	cert := license.IssueCert("root1", rootPriv, "online-1", onPub, t0.Add(90*24*time.Hour))
	_, inst, _ := ed25519.GenerateKey(rand.Reader)
	r := &rig{t: t, st: st, srv: NewServer(st, onPriv, cert), rootPub: rootPub, instance: inst, now: t0}
	st.now = func() time.Time { return r.now }
	r.srv.Now = func() time.Time { return r.now }
	return r
}

func (r *rig) issue(order string) string {
	r.t.Helper()
	key, err := r.st.Issue("owner", order, "pro", 10, t0.Add(30*24*time.Hour), "buyer@example.com")
	if err != nil {
		r.t.Fatal(err)
	}
	return key
}

func (r *rig) request(key string, priv ed25519.PrivateKey) (string, int64, error) {
	r.t.Helper()
	inst, sig, err := SignRequest(priv, key, r.now.Unix())
	if err != nil {
		return "", 0, err
	}
	return r.srv.Token(key, inst, r.now.Unix(), sig)
}

func TestKeyFormat(t *testing.T) {
	display, norm := NewKey()
	if !strings.HasPrefix(display, "SKH-") || len(norm) != 28 {
		t.Fatalf("unexpected key %q / %q", display, norm)
	}
	got, ok := ParseKey(strings.ToLower(display))
	if !ok || got != norm {
		t.Error("a key typed in lower case must still parse")
	}
	if _, ok := ParseKey(strings.ReplaceAll(display, "-", " ")); !ok {
		t.Error("spaces instead of dashes must still parse")
	}
	typo := []byte(display)
	typo[6] ^= 1
	if _, ok := ParseKey(string(typo)); ok && string(typo) != display {
		t.Error("a typo passed the checksum")
	}
	for _, bad := range []string{"", "SKH", "SKH-AAAA", strings.Repeat("A", 28), "SKH-" + strings.Repeat("1", 28)} {
		if _, ok := ParseKey(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	d2, _ := NewKey()
	if d2 == display {
		t.Error("keys must be unique")
	}
}

func TestActivateAndVerifyWithRootOnly(t *testing.T) {
	r := newRig(t)
	key := r.issue("FP-1001")
	tok, exp, err := r.request(key, r.instance)
	if err != nil {
		t.Fatal(err)
	}
	if want := r.now.Add(7 * 24 * time.Hour).Unix(); exp != want {
		t.Errorf("expiry %d, want %d", exp, want)
	}
	// a panel trusts only the root key
	v := &license.Verifier{Roots: map[string]ed25519.PublicKey{"root1": r.rootPub}}
	inst, _, _ := SignRequest(r.instance, key, 0)
	c, st, err := v.Check(tok, inst, r.now.Add(time.Minute))
	if err != nil || st != license.Active || c.Plan != "pro" || c.MaxServers != 10 {
		t.Fatalf("panel check: %v %v %+v", st, err, c)
	}
}

func TestTokenNeverOutlivesTheLicense(t *testing.T) {
	r := newRig(t)
	key, _ := r.st.Issue("owner", "FP-2", "pro", 5, r.now.Add(2*24*time.Hour), "")
	_, exp, err := r.request(key, r.instance)
	if err != nil {
		t.Fatal(err)
	}
	if want := r.now.Add(2 * 24 * time.Hour).Unix(); exp != want {
		t.Errorf("token expires %d, licence ends %d", exp, want)
	}
}

func TestOnlyTheKeyStoredIsAHash(t *testing.T) {
	r := newRig(t)
	key := r.issue("FP-3")
	norm, _ := ParseKey(key)
	var stored string
	r.st.db.QueryRow(`select key_hash from licenses`).Scan(&stored)
	if stored == norm || strings.Contains(stored, norm) || stored != hashKey(norm) {
		t.Error("the database must hold only a hash of the key")
	}
}

func TestRefusals(t *testing.T) {
	r := newRig(t)
	key := r.issue("FP-4")
	_, otherInst, _ := ed25519.GenerateKey(rand.Reader)
	other, _ := NewKey()

	if _, _, err := r.request(other, r.instance); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown key: %v", err)
	}
	if _, _, err := r.request("SKH-NOT-A-KEY", r.instance); !errors.Is(err, ErrInvalid) {
		t.Errorf("malformed key: %v", err)
	}
	// signature made by a different instance than the one named in the request
	inst, _, _ := SignRequest(r.instance, key, r.now.Unix())
	_, sig, _ := SignRequest(otherInst, key, r.now.Unix())
	if _, _, err := r.srv.Token(key, inst, r.now.Unix(), sig); !errors.Is(err, ErrInvalid) {
		t.Errorf("signature from another key: %v", err)
	}
	// stale and future timestamps
	for _, skew := range []time.Duration{-10 * time.Minute, 10 * time.Minute} {
		inst, sig, _ := SignRequest(r.instance, key, r.now.Add(skew).Unix())
		if _, _, err := r.srv.Token(key, inst, r.now.Add(skew).Unix(), sig); !errors.Is(err, ErrInvalid) {
			t.Errorf("skew %v: %v", skew, err)
		}
	}
}

func TestReplayAndBinding(t *testing.T) {
	r := newRig(t)
	key := r.issue("FP-5")
	if _, _, err := r.request(key, r.instance); err != nil {
		t.Fatal(err)
	}
	// the very same signed request again (same timestamp) is a replay
	inst, sig, _ := SignRequest(r.instance, key, r.now.Unix())
	if _, _, err := r.srv.Token(key, inst, r.now.Unix(), sig); !errors.Is(err, ErrInvalid) {
		t.Errorf("replay: %v", err)
	}
	// a later renewal from the same instance works
	r.now = r.now.Add(24 * time.Hour)
	if _, _, err := r.request(key, r.instance); err != nil {
		t.Errorf("renewal: %v", err)
	}
	// a second instance cannot take over
	_, second, _ := ed25519.GenerateKey(rand.Reader)
	r.now = r.now.Add(time.Hour)
	if _, _, err := r.request(key, second); !errors.Is(err, ErrBound) {
		t.Errorf("second instance: %v", err)
	}
	// until the owner resets the binding
	if err := r.st.ResetBinding("owner", "FP-5"); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Hour)
	if _, _, err := r.request(key, second); err != nil {
		t.Errorf("after reset: %v", err)
	}
}

func TestRevokeAndExpiry(t *testing.T) {
	r := newRig(t)
	key := r.issue("FP-6")
	if _, _, err := r.request(key, r.instance); err != nil {
		t.Fatal(err)
	}
	r.st.Revoke("owner", "FP-6")
	r.now = r.now.Add(time.Hour)
	if _, _, err := r.request(key, r.instance); !errors.Is(err, ErrRevoked) {
		t.Errorf("revoked: %v", err)
	}
	key2 := r.issue("FP-7")
	r.now = r.now.Add(31 * 24 * time.Hour)
	if _, _, err := r.request(key2, r.instance); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	// extending brings it back
	r.st.Extend("owner", "FP-7", r.now.Add(10*24*time.Hour))
	r.now = r.now.Add(time.Hour)
	if _, _, err := r.request(key2, r.instance); err != nil {
		t.Errorf("after extend: %v", err)
	}
}

func TestOneOrderOneLicense(t *testing.T) {
	r := newRig(t)
	r.issue("FP-8")
	if _, err := r.st.Issue("helper", "FP-8", "pro", 10, t0.Add(time.Hour*24), ""); !errors.Is(err, ErrExists) {
		t.Errorf("second key for one order: %v", err)
	}
	for _, bad := range []struct {
		order string
		n     int
		exp   time.Time
	}{{"", 10, t0.Add(time.Hour)}, {"FP-9", 0, t0.Add(time.Hour)}, {"FP-9", 5, t0.Add(-time.Hour)}} {
		if _, err := r.st.Issue("owner", bad.order, "pro", bad.n, bad.exp, ""); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestAuditChainDetectsEditing(t *testing.T) {
	r := newRig(t)
	r.issue("FP-10")
	r.issue("FP-11")
	r.st.Revoke("owner", "FP-10")
	if bad, err := r.st.VerifyAudit(); err != nil || bad != 0 {
		t.Fatalf("intact chain reported broken at %d: %v", bad, err)
	}
	// someone rewrites what happened
	r.st.db.Exec(`update audit set detail='order=FP-99' where id=2`)
	if bad, _ := r.st.VerifyAudit(); bad != 2 {
		t.Errorf("edit not detected, first bad row = %d", bad)
	}
	r2 := newRig(t)
	r2.issue("A")
	r2.issue("B")
	r2.issue("C")
	r2.st.db.Exec(`delete from audit where id=2`)
	if bad, _ := r2.st.VerifyAudit(); bad == 0 {
		t.Error("a deleted row was not detected")
	}
}

func post(r *rig, body any) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/token", bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.9:1"
	rec := httptest.NewRecorder()
	r.srv.Handler().ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestHTTP(t *testing.T) {
	r := newRig(t)
	key := r.issue("FP-12")
	inst, sig, _ := SignRequest(r.instance, key, r.now.Unix())
	code, out := post(r, tokenRequest{Key: key, Inst: inst, TS: r.now.Unix(), Sig: sig})
	if code != 200 || out["token"] == nil {
		t.Fatalf("%d %v", code, out)
	}
	code, out = post(r, tokenRequest{Key: key, Inst: inst, TS: r.now.Unix(), Sig: sig}) // replay
	if code != 403 || out["error"] != "invalid_license" {
		t.Errorf("replay over HTTP: %d %v", code, out)
	}
	if code, _ := post(r, map[string]any{"key": key, "extra": 1}); code != 400 {
		t.Errorf("unknown fields must be refused: %d", code)
	}
	r.st.Revoke("owner", "FP-12")
	r.now = r.now.Add(time.Hour)
	inst, sig, _ = SignRequest(r.instance, key, r.now.Unix())
	if code, out := post(r, tokenRequest{Key: key, Inst: inst, TS: r.now.Unix(), Sig: sig}); code != 403 || out["error"] != "license_revoked" {
		t.Errorf("revoked over HTTP: %d %v", code, out)
	}
	// a GET is not an activation
	req := httptest.NewRequest("GET", "/v1/token", nil)
	rec := httptest.NewRecorder()
	r.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}

func TestHTTPRateLimit(t *testing.T) {
	r := newRig(t)
	var last int
	for i := 0; i < 35; i++ {
		last, _ = post(r, tokenRequest{Key: "SKH-JUNK"})
	}
	if last != 429 {
		t.Errorf("35th request from one address: %d, want 429", last)
	}
}
