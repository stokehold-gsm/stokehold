//go:build linux

package safefs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Only meaningful as root (the helper's situation): created files must end up
// owned by the game user, not by root.
func TestCreatedFilesBelongToTheGameUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const uid, gid = 65534, 65534
	f.SetOwner(uid, gid)
	if err := f.Mkdir("plugins"); err != nil {
		t.Fatal(err)
	}
	if err := f.PutChunk("plugins/a.jar", 0, []byte("jar"), true); err != nil {
		t.Fatal(err)
	}
	if err := f.Put("plugins/b.jar", stringsReader("jar2"), 100, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"plugins", "plugins/a.jar", "plugins/b.jar"} {
		st, err := os.Lstat(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		if sys := st.Sys().(*syscall.Stat_t); int(sys.Uid) != uid || int(sys.Gid) != gid {
			t.Errorf("%s is owned by %d:%d, want %d:%d", p, sys.Uid, sys.Gid, uid, gid)
		}
	}
}
