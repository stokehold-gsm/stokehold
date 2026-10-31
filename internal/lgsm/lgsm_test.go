package lgsm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Captured from LinuxGSM v26.4.0 on Ubuntu 22.04.
const checkUpdateOut = "\x1b[1m\x1b[93mChecking for update\x1b[0m\n\nNo update available\n* Local build: 26.3\n* Remote build: 26.3\n* Branch: release\n"
const detailsOut = "Java:         openjdk version \"25.0.4.1\"\nStatus:         \x1b[1m\x1b[32mSTARTED\x1b[0m\n"

func TestParseCheckUpdate(t *testing.T) {
	u, err := ParseCheckUpdate(checkUpdateOut)
	if err != nil || u.Available || u.Local != "26.3" || u.Remote != "26.3" || u.Branch != "release" {
		t.Fatalf("got %+v %v", u, err)
	}
	if u, _ := ParseCheckUpdate("Update available\n* Local build: 1\n* Remote build: 2\n"); !u.Available {
		t.Error("positive form not detected")
	}
	if _, err := ParseCheckUpdate("something else entirely"); err == nil {
		t.Error("unknown output must be an error, not 'no update'")
	}
}

func TestParseStatus(t *testing.T) {
	if s, err := ParseStatus("Status:         STARTED\n"); err != nil || s != "STARTED" {
		t.Fatalf("plain: %q %v", s, err)
	}
	if _, err := ParseStatus("nothing"); err == nil {
		t.Error("missing status must be an error")
	}
}

func TestDiscover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no executable bit")
	}
	home := t.TempDir()
	mk := func(name string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(home, name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	mk("mcserver", 0o755)
	mk("vhserver", 0o755)   // no config dir: not adopted
	mk("notes.txt", 0o755)  // wrong name
	mk("rustserver", 0o644) // not executable
	if err := os.MkdirAll(filepath.Join(home, "lgsm", "config-lgsm", "mcserver"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "lgsm", "config-lgsm", "rustserver"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(home)
	if err != nil || len(got) != 1 || got[0].Name != "mcserver" {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestRunRejects(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"mcserver", "rm -rf /"}, {"mcserver", "install"}, {"mcserver", "console"},
		{"../mcserver", "start"}, {"bash", "start"}, {"mc server", "start"}, {"", "start"},
	} {
		if _, err := Run(context.Background(), Server{Name: tc.name, Dir: t.TempDir()}, tc.cmd); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%q %q: want ErrNotAllowed, got %v", tc.name, tc.cmd, err)
		}
	}
}

func TestRunExecutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '\\033[32mok %s\\033[0m' \"$1\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mcserver"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), Server{Name: "mcserver", Dir: dir}, "details")
	if err != nil || out != "ok details" {
		t.Fatalf("%q %v", out, err)
	}
}
