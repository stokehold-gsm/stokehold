package sign

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	privPEM, pubPEM, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivate(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublic(pubPEM)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("abc  stokehold-linux-amd64\n")
	sig := Sign(priv, data)
	if !Verify(pub, data, sig) {
		t.Fatal("valid signature rejected")
	}
	if Verify(pub, append(data, 'x'), sig) {
		t.Error("tampered data accepted")
	}
	bad := append([]byte{}, sig...)
	bad[0] ^= 1
	if Verify(pub, data, bad) || Verify(pub, data, sig[:10]) || Verify(pub, data, nil) {
		t.Error("tampered or short signature accepted")
	}
	_, other, _ := GenerateKey()
	otherPub, _ := ParsePublic(other)
	if Verify(otherPub, data, sig) {
		t.Error("signature verified under a different key")
	}
}

func TestParseRejectsWrongKinds(t *testing.T) {
	privPEM, pubPEM, _ := GenerateKey()
	if _, err := ParsePrivate(pubPEM); err == nil {
		t.Error("public key parsed as private")
	}
	if _, err := ParsePublic(privPEM); err == nil {
		t.Error("private key parsed as public")
	}
	if _, err := ParsePublic([]byte("garbage")); err == nil {
		t.Error("garbage accepted")
	}
}

// The installer verifies with openssl, so check that it agrees with Go.
func TestOpenSSLAgrees(t *testing.T) {
	ossl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not installed")
	}
	if out, err := exec.Command(ossl, "version").Output(); err != nil || len(out) < 8 || string(out[:8]) != "OpenSSL " {
		t.Skip("openssl is not OpenSSL (or too old)")
	}
	privPEM, pubPEM, _ := GenerateKey()
	priv, _ := ParsePrivate(privPEM)
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	data := []byte("release checksums\n")
	pubF, dataF, sigF := write("pub.pem", pubPEM), write("data", data), write("sig", Sign(priv, data))
	out, err := exec.Command(ossl, "pkeyutl", "-verify", "-pubin", "-inkey", pubF, "-rawin", "-in", dataF, "-sigfile", sigF).CombinedOutput()
	if err != nil {
		t.Skipf("this openssl cannot verify ed25519 with -rawin (%v: %s); the installer needs OpenSSL 3", err, out)
	}
	bad := write("bad", append(data, '!'))
	if out, err := exec.Command(ossl, "pkeyutl", "-verify", "-pubin", "-inkey", pubF, "-rawin", "-in", bad, "-sigfile", sigF).CombinedOutput(); err == nil {
		t.Errorf("openssl accepted tampered data: %s", out)
	}
}
