package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T) (ed25519.PrivateKey, *Verifier, Claims) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v := &Verifier{Keys: map[string]ed25519.PublicKey{"k1": pub}, Revoked: map[string]bool{}}
	c := Claims{KID: "k1", JTI: "j", IAT: t0.Unix(), EXP: t0.Add(7 * 24 * time.Hour).Unix(), Inst: "inst-A", Plan: "pro", Features: []string{FeatureS3Backup}}
	return priv, v, c
}

func TestCheck(t *testing.T) {
	priv, v, c := fixture(t)
	tok := Sign(priv, c)
	day := 24 * time.Hour

	cases := []struct {
		name  string
		token string
		inst  string
		now   time.Time
		want  State
		err   error
	}{
		{"active", tok, "inst-A", t0.Add(day), Active, nil},
		{"grace", tok, "inst-A", t0.Add(20 * day), InGrace, nil},
		{"expired", tok, "inst-A", t0.Add(60 * day), Free, ErrExpired},
		{"other instance", tok, "inst-B", t0.Add(day), Free, ErrInvalid},
		{"garbage", "abc", "inst-A", t0, Free, ErrInvalid},
		{"tampered payload", "x" + tok, "inst-A", t0, Free, ErrInvalid},
		{"issued in the future", Sign(priv, Claims{KID: "k1", IAT: t0.Add(time.Hour).Unix(), EXP: t0.Add(day).Unix(), Inst: "inst-A"}), "inst-A", t0, Free, ErrInvalid},
	}
	for _, tc := range cases {
		v.LastSeen = 0
		_, st, err := v.Check(tc.token, tc.inst, tc.now)
		if st != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: got %v %v, want %v %v", tc.name, st, err, tc.want, tc.err)
		}
	}
}

func TestWrongKeyAndUnknownKID(t *testing.T) {
	_, v, c := fixture(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, st, _ := v.Check(Sign(other, c), "inst-A", t0); st != Free {
		t.Error("token signed by another key accepted")
	}
	c.KID = "unknown"
	priv, _, _ := fixture(t)
	if _, st, _ := v.Check(Sign(priv, c), "inst-A", t0); st != Free {
		t.Error("unknown kid accepted")
	}
}

func TestRevokedKID(t *testing.T) {
	priv, v, c := fixture(t)
	v.Revoked["k1"] = true
	if _, st, _ := v.Check(Sign(priv, c), "inst-A", t0); st != Free {
		t.Error("revoked kid accepted")
	}
}

func TestClockRollbackDoesNotExtend(t *testing.T) {
	priv, v, c := fixture(t)
	tok := Sign(priv, c)
	// we saw the clock at day 40, then the user winds it back to day 1
	v.LastSeen = t0.Add(40 * 24 * time.Hour).Unix()
	if _, st, _ := v.Check(tok, "inst-A", t0.Add(24*time.Hour)); st != Free {
		t.Errorf("rolled-back clock extended an expired token: %v", st)
	}
}

func TestDomainSeparation(t *testing.T) {
	priv, v, c := fixture(t)
	tok := Sign(priv, c)
	p, _, _ := strings.Cut(tok, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(p)
	bare := ed25519.Sign(priv, payload)
	forged := p + "." + base64.RawURLEncoding.EncodeToString(bare)
	if _, st, _ := v.Check(forged, "inst-A", t0); st != Free {
		t.Error("signature without domain prefix accepted")
	}
}
