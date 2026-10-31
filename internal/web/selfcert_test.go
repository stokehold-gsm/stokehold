package web

import (
	"crypto/x509"
	"testing"
	"time"
)

func TestSelfSignedCertIsKeptAndNamesLocalhost(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c, fp, err := SelfSignedCert(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(c.Certificate[0])
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Errorf("the certificate must cover 127.0.0.1: %v", err)
	}
	_, fp2, err := SelfSignedCert(dir, now)
	if err != nil || fp2 != fp {
		t.Fatalf("a valid certificate must be reused: %v %s %s", err, fp, fp2)
	}
	if _, fp3, _ := SelfSignedCert(dir, now.Add(selfCertLife)); fp3 == fp {
		t.Error("an expiring certificate must be replaced")
	}
}
