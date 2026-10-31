package safefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T) (*FS, string) {
	t.Helper()
	base := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(base, "serverfiles", "config", "l1", "l2", "l3", "l4", "l5"), 0o755))
	must(os.MkdirAll(filepath.Join(base, "lgsm", "config-lgsm", "mcserver"), 0o755))
	must(os.MkdirAll(filepath.Join(base, ".ssh"), 0o700))
	write := func(rel, s string, mode os.FileMode) {
		must(os.WriteFile(filepath.Join(base, rel), []byte(s), mode))
	}
	write("serverfiles/server.properties", "motd=hi\n", 0o640)
	write("serverfiles/config/game.cfg", "a=1\n", 0o644)
	write("serverfiles/binary.dat", "x", 0o644)
	write("serverfiles/config/l1/l2/l3/l4/l5/too-deep.cfg", "x", 0o644)
	write("lgsm/config-lgsm/mcserver/mcserver.cfg", "port=1\n", 0o644)
	write(".ssh/authorized_keys.conf", "secret", 0o600)
	write("outside.cfg", "nope", 0o644)
	f, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, base
}

func TestListShowsOnlyEditableFilesInTheAllowedTrees(t *testing.T) {
	f, _ := tree(t)
	got, err := f.List([]string{"serverfiles", "lgsm/config-lgsm"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := "lgsm/config-lgsm/mcserver/mcserver.cfg,serverfiles/config/game.cfg,serverfiles/server.properties"
	if strings.Join(got, ",") != want {
		t.Errorf("got %v", got)
	}
	for _, p := range got {
		if strings.Contains(p, ".ssh") || p == "outside.cfg" {
			t.Errorf("listed a file outside the trees: %s", p)
		}
	}
	if limited, _ := f.List([]string{"serverfiles"}, 1); len(limited) != 1 {
		t.Errorf("the limit was ignored: %v", limited)
	}
	if none, err := f.List([]string{"no-such-dir"}, 10); err != nil || len(none) != 0 {
		t.Errorf("a missing tree: %v %v", none, err)
	}
	for _, bad := range []string{"../x", "/etc", `a\b`, ""} {
		if _, err := f.List([]string{bad}, 10); !errors.Is(err, ErrDenied) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestListDoesNotFollowSymlinks(t *testing.T) {
	f, base := tree(t)
	if err := os.Symlink(base, filepath.Join(base, "serverfiles", "escape")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	os.Symlink(filepath.Join(base, "outside.cfg"), filepath.Join(base, "serverfiles", "link.cfg"))
	got, _ := f.List([]string{"serverfiles"}, 100)
	for _, p := range got {
		if strings.Contains(p, "escape") || strings.Contains(p, "link.cfg") {
			t.Errorf("followed a symlink: %s", p)
		}
	}
}

func TestReplaceKeepsModeMakesABackupAndLeavesNoTempFiles(t *testing.T) {
	f, base := tree(t)
	p := "serverfiles/server.properties"
	if err := f.Replace(p, []byte("motd=changed\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(base, p))
	if string(b) != "motd=changed\n" {
		t.Errorf("content: %q", b)
	}
	bak, _ := os.ReadFile(filepath.Join(base, p+".stokehold-bak"))
	if string(bak) != "motd=hi\n" {
		t.Errorf("backup: %q", bak)
	}
	if info, _ := os.Stat(filepath.Join(base, p)); info.Mode().Perm() != 0o640 && os.PathSeparator == '/' {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Join(base, "serverfiles"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	// the backup holds the previous version, so a second save rotates it
	f.Replace(p, []byte("motd=third\n"))
	bak, _ = os.ReadFile(filepath.Join(base, p+".stokehold-bak"))
	if string(bak) != "motd=changed\n" {
		t.Errorf("backup did not rotate: %q", bak)
	}
}

func TestReplaceRefusesWhatItShould(t *testing.T) {
	f, base := tree(t)
	for name, c := range map[string]struct {
		path string
		data string
	}{
		"new file":        {"serverfiles/brand-new.cfg", "x"},
		"wrong extension": {"serverfiles/binary.dat", "x"},
		"script":          {"serverfiles/run.sh", "x"},
		"parent":          {"../outside.cfg", "x"},
		"absolute":        {"/etc/passwd", "x"},
		"backslash":       {`serverfiles\server.properties`, "x"},
		"nul in name":     {"serverfiles/server.properties\x00.cfg", "x"},
		"binary content":  {"serverfiles/server.properties", "a\x00b"},
		"invalid utf8":    {"serverfiles/server.properties", "\xff\xfe"},
		"too large":       {"serverfiles/server.properties", strings.Repeat("a", MaxEditSize+1)},
		"directory":       {"serverfiles/config", "x"},
	} {
		if err := f.Replace(c.path, []byte(c.data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(base, "serverfiles/server.properties")); string(b) != "motd=hi\n" {
		t.Errorf("a refused write changed the file: %q", b)
	}
	if _, err := os.Stat(filepath.Join(base, "serverfiles/brand-new.cfg")); err == nil {
		t.Error("a new file was created")
	}
	if _, err := os.Stat(filepath.Join(base, "outside.cfg.stokehold-bak")); err == nil {
		t.Error("a backup was written outside the tree")
	}
}

func TestReplaceNeverWritesThroughSymlinks(t *testing.T) {
	f, base := tree(t)
	if err := os.Symlink(filepath.Join(base, "outside.cfg"), filepath.Join(base, "serverfiles", "link.cfg")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	os.Symlink(base, filepath.Join(base, "serverfiles", "dir"))
	for _, p := range []string{"serverfiles/link.cfg", "serverfiles/dir/outside.cfg"} {
		if err := f.Replace(p, []byte("pwned")); err == nil {
			t.Errorf("%s: wrote through a symlink", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(base, "outside.cfg")); string(b) != "nope" {
		t.Errorf("the file outside was changed: %q", b)
	}
}

func TestValidText(t *testing.T) {
	for in, want := range map[string]bool{"": true, "plain\nline": true, "юникод": true, "a\x00b": false, "\xff": false} {
		if got := ValidText([]byte(in)); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}
