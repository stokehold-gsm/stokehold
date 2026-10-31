package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stokehold-gsm/stokehold/pkg/sign"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cur, cand string
		want      bool
	}{
		{"v0.1.2", "v0.1.3", true}, {"v0.1.3", "v0.1.3", false}, {"v0.2.0", "v0.1.9", false},
		{"v1.0.0-beta.1", "v1.0.0", true}, {"v1.0.0", "v1.0.0-beta.2", false}, {"v1.0.0-beta.1", "v1.0.0-beta.2", true},
		{"dev", "v9.9.9", false}, {"v0.1.0", "nonsense", false}, {"v0.9.0", "v0.10.0", true},
	} {
		if got := Newer(c.cur, c.cand); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.cur, c.cand, got)
		}
	}
}

func TestValidTag(t *testing.T) {
	for _, s := range []string{"v0.1.3", "v1.2.3-beta.1"} {
		if !ValidTag(s) {
			t.Errorf("%q must be valid", s)
		}
	}
	for _, s := range []string{"", "0.1.3", "v1", "v1.2.3; rm -rf /", "v1.2.3/../x", "v1.2.3\n"} {
		if ValidTag(s) {
			t.Errorf("%q must be rejected", s)
		}
	}
}

func TestReleasePublicKeyMatchesTheInstaller(t *testing.T) {
	b, err := os.ReadFile("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)<<'PEM'.*?(-----BEGIN PUBLIC KEY-----.*?-----END PUBLIC KEY-----)`).FindSubmatch(b)
	if m == nil || strings.TrimSpace(string(m[1])) != strings.TrimSpace(releasePEM) {
		t.Fatal("selfupdate.releasePEM differs from BUILTIN_PUBKEY in install/install.sh")
	}
}

func TestLatestPicksTheChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"v0.3.0-beta.1","prerelease":true,"published_at":"2026-11-02T00:00:00Z"},
			{"tag_name":"v0.2.9","draft":true},{"tag_name":"v0.2.1","published_at":"2026-11-01T00:00:00Z","body":"notes"},{"tag_name":"bad tag"}]`))
	}))
	defer srv.Close()
	st, err := Latest(context.Background(), srv.Client(), srv.URL, "stable")
	if err != nil || st.Tag != "v0.2.1" || st.Notes != "notes" {
		t.Fatalf("stable: %+v %v", st, err)
	}
	if be, _ := Latest(context.Background(), srv.Client(), srv.URL, "beta"); be == nil || be.Tag != "v0.3.0-beta.1" {
		t.Fatalf("beta: %+v", be)
	}
}

func release(t *testing.T, arch string, tamper func(map[string][]byte)) (base string, pub ed25519.PublicKey, stop func()) {
	t.Helper()
	pubK, priv, _ := ed25519.GenerateKey(rand.Reader)
	files := map[string][]byte{
		"install.sh": []byte("#!/bin/sh"), "stokehold-linux-" + arch: []byte("panel"), "stokehold-helper-linux-" + arch: []byte("helper"),
	}
	var sums strings.Builder
	for n, b := range files {
		h := sha256.Sum256(b)
		sums.WriteString(hex.EncodeToString(h[:]) + "  " + n + "\n")
	}
	files["SHA256SUMS"] = []byte(sums.String())
	files["SHA256SUMS.sig"] = sign.Sign(priv, files["SHA256SUMS"])
	if tamper != nil {
		tamper(files)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := files[filepath.Base(r.URL.Path)]; ok && strings.HasPrefix(r.URL.Path, "/v1.2.3/") {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	return srv.URL, pubK, srv.Close
}

func TestStageVerifiesSignatureAndHashes(t *testing.T) {
	base, pub, stop := release(t, "amd64", nil)
	defer stop()
	dir := t.TempDir()
	if err := Stage(context.Background(), http.DefaultClient, base, "v1.2.3", "amd64", dir); err == nil {
		t.Fatal("a release signed by another key must be refused")
	}
	if err := verifyWith(pub, dir, "amd64"); err != nil {
		t.Fatalf("a good release must verify with its own key: %v", err)
	}
	b2, pub2, stop2 := release(t, "amd64", func(f map[string][]byte) { f["stokehold-linux-amd64"] = []byte("evil") })
	defer stop2()
	d2 := t.TempDir()
	Stage(context.Background(), http.DefaultClient, b2, "v1.2.3", "amd64", d2)
	if err := verifyWith(pub2, d2, "amd64"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a swapped binary must fail the checksum: %v", err)
	}
	if err := Stage(context.Background(), http.DefaultClient, base, "v1.2.3; x", "amd64", t.TempDir()); err == nil {
		t.Fatal("an odd tag must be refused before any request")
	}
}
