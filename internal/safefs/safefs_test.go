package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) (*FS, string) {
	t.Helper()
	base := t.TempDir()
	srv := filepath.Join(base, "srv")
	if err := os.Mkdir(srv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret.cfg"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv, "server.cfg"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Open(srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, base
}

func TestReadInside(t *testing.T) {
	f, _ := setup(t)
	b, err := f.ReadFile("server.cfg", 1024)
	if err != nil || string(b) != "ok" {
		t.Fatalf("read: %q %v", b, err)
	}
}

func TestHostilePaths(t *testing.T) {
	f, _ := setup(t)
	for _, p := range []string{
		"../secret.cfg", "a/../../secret.cfg", "/etc/passwd", `..\secret.cfg`,
		"server.cfg\x00.txt", "", ".", "./server.cfg", "a//b.cfg",
	} {
		if _, err := f.ReadFile(p, 1024); !errors.Is(err, ErrDenied) {
			t.Errorf("read %q: want ErrDenied, got %v", p, err)
		}
	}
}

func TestSymlinkEscape(t *testing.T) {
	f, base := setup(t)
	if err := os.Symlink(filepath.Join(base, "secret.cfg"), filepath.Join(base, "srv", "link.cfg")); err != nil {
		t.Skip("symlinks unavailable here:", err)
	}
	if err := os.Symlink(base, filepath.Join(base, "srv", "dir")); err != nil {
		t.Skip("symlinks unavailable here:", err)
	}
	if b, err := f.ReadFile("link.cfg", 1024); err == nil {
		t.Errorf("file symlink escaped: %q", b)
	}
	if b, err := f.ReadFile("dir/secret.cfg", 1024); err == nil {
		t.Errorf("dir symlink escaped: %q", b)
	}
}
