package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

type chain struct {
	rootPub ed25519.PublicKey
	online  ed25519.PrivateKey
	cert    Cert
	v       *Verifier
}

func newChain(t *testing.T) chain {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(rand.Reader)
	onPub, onPriv, _ := ed25519.GenerateKey(rand.Reader)
	cert := IssueCert("root1", rootPriv, "online-1", onPub, t0.Add(90*24*time.Hour))
	return chain{rootPub, onPriv, cert, &Verifier{Roots: map[string]ed25519.PublicKey{"root1": rootPub}, Revoked: map[string]bool{}}}
}

func (c chain) token(cert Cert, iat time.Time) string {
	return Sign(c.online, Claims{KID: cert.KID, JTI: "j", IAT: iat.Unix(), EXP: iat.Add(7 * 24 * time.Hour).Unix(), Inst: "inst-A", Plan: "pro", MaxServers: 10, Cert: &cert})
}

func TestCertChainAccepted(t *testing.T) {
	c := newChain(t)
	if _, st, err := c.v.Check(c.token(c.cert, t0), "inst-A", t0.Add(time.Hour)); st != Active || err != nil {
		t.Fatalf("%v %v", st, err)
	}
}

func TestCertFromUntrustedRootRejected(t *testing.T) {
	c := newChain(t)
	_, otherRoot, _ := ed25519.GenerateKey(rand.Reader)
	onPub := c.online.Public().(ed25519.PublicKey)
	forged := IssueCert("root1", otherRoot, "online-1", onPub, t0.Add(90*24*time.Hour))
	if _, st, _ := c.v.Check(c.token(forged, t0), "inst-A", t0); st != Free {
		t.Error("a certificate signed by a different root was accepted")
	}
	unknownRoot := c.cert
	unknownRoot.Root = "root2"
	if _, st, _ := c.v.Check(c.token(unknownRoot, t0), "inst-A", t0); st != Free {
		t.Error("a certificate naming an unknown root was accepted")
	}
}

func TestSwappedOnlineKeyRejected(t *testing.T) {
	// an attacker signs with their own key but staples a real certificate
	c := newChain(t)
	_, attacker, _ := ed25519.GenerateKey(rand.Reader)
	tok := Sign(attacker, Claims{KID: c.cert.KID, IAT: t0.Unix(), EXP: t0.Add(time.Hour).Unix(), Inst: "inst-A", Plan: "pro", Cert: &c.cert})
	if _, st, _ := c.v.Check(tok, "inst-A", t0); st != Free {
		t.Error("token signed by a key the certificate does not cover was accepted")
	}
}

func TestTamperedCertFieldsRejected(t *testing.T) {
	c := newChain(t)
	for name, mut := range map[string]func(*Cert){
		"later expiry": func(x *Cert) { x.NotAfter += 10_000_000 },
		"other kid":    func(x *Cert) { x.KID = "online-2" },
		"other key": func(x *Cert) {
			p, _, _ := ed25519.GenerateKey(rand.Reader)
			x.Pub = IssueCert("r", ed25519.NewKeyFromSeed(make([]byte, 32)), "k", p, t0).Pub
		},
	} {
		cert := c.cert
		mut(&cert)
		if _, st, _ := c.v.Check(c.token(cert, t0), "inst-A", t0); st != Free {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTokenIssuedAfterCertExpiryRejectedButOlderOnesSurvive(t *testing.T) {
	c := newChain(t)
	after := time.Unix(c.cert.NotAfter, 0).Add(time.Hour)
	if _, st, _ := c.v.Check(c.token(c.cert, after), "inst-A", after); st != Free {
		t.Error("a token issued after the certificate expired was accepted")
	}
	issued := time.Unix(c.cert.NotAfter, 0).Add(-time.Hour)
	// checked a few days later, once the certificate itself has expired
	if _, st, err := c.v.Check(c.token(c.cert, issued), "inst-A", issued.Add(3*24*time.Hour)); st != Active || err != nil {
		t.Errorf("a token issued while the certificate was valid must keep working: %v %v", st, err)
	}
}

func TestRevokedOnlineKeyRejected(t *testing.T) {
	c := newChain(t)
	c.v.Revoked["online-1"] = true
	if _, st, _ := c.v.Check(c.token(c.cert, t0), "inst-A", t0); st != Free {
		t.Error("a token from a revoked online key was accepted")
	}
}
