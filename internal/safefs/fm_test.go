package safefs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDirSortsAndHidesSensitiveNames(t *testing.T) {
	f, base := setup(t)
	srv := filepath.Join(base, "srv")
	os.Mkdir(filepath.Join(srv, "zdir"), 0o755)
	os.Mkdir(filepath.Join(srv, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(srv, ".bashrc"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(srv, "a.txt"), []byte("12345"), 0o644)
	list, more, err := f.ListDir(".", 100)
	if err != nil || more {
		t.Fatal(err, more)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Type+":"+e.Name)
	}
	if got := strings.Join(names, " "); got != "d:zdir f:a.txt f:server.cfg" {
		t.Errorf("directories first, then names, no .ssh or .bashrc: %s", got)
	}
	if list[1].Size != 5 {
		t.Errorf("size: %+v", list[1])
	}
	if cut, more, _ := f.ListDir(".", 2); len(cut) != 2 || !more {
		t.Errorf("limit: %v %v", cut, more)
	}
	if _, _, err := f.ListDir("server.cfg", 10); !errors.Is(err, ErrDenied) {
		t.Errorf("a file is not a directory: %v", err)
	}
}

func TestFileManagerRefusesHostileAndSensitivePaths(t *testing.T) {
	f, _ := setup(t)
	for _, p := range []string{"../secret.cfg", "/etc/passwd", "a/../../x", "", "a//b", ".ssh", ".ssh/authorized_keys", "sub/.BASHRC", "x/authorized_keys", `a\b`} {
		if err := f.Mkdir(p); !errors.Is(err, ErrDenied) {
			t.Errorf("mkdir %q: %v", p, err)
		}
		if err := f.PutChunk(p, 0, []byte("x"), true); !errors.Is(err, ErrDenied) {
			t.Errorf("put %q: %v", p, err)
		}
		if err := f.Remove(p); !errors.Is(err, ErrDenied) {
			t.Errorf("remove %q: %v", p, err)
		}
		if _, _, err := f.ReadAt(p, 0, 10); !errors.Is(err, ErrDenied) {
			t.Errorf("read %q: %v", p, err)
		}
		if err := f.Move("server.cfg", p); !errors.Is(err, ErrDenied) {
			t.Errorf("move to %q: %v", p, err)
		}
	}
	for _, p := range []string{".", "..", "./"} {
		if err := f.Remove(p); !errors.Is(err, ErrDenied) {
			t.Errorf("the root itself must not be removed (%q): %v", p, err)
		}
	}
}

func TestSymlinksAreNeverFollowedOut(t *testing.T) {
	f, base := setup(t)
	srv := filepath.Join(base, "srv")
	if err := os.Symlink(base, filepath.Join(srv, "out")); err != nil {
		t.Skip("symlinks unavailable here:", err)
	}
	os.Symlink(filepath.Join(base, "secret.cfg"), filepath.Join(srv, "link.cfg"))
	if _, _, err := f.ListDir("out", 10); err == nil {
		t.Error("listed through a symlink that leaves the root")
	}
	if _, _, err := f.ReadAt("link.cfg", 0, 10); err == nil {
		t.Error("read through a file symlink that leaves the root")
	}
	if err := f.PutChunk("out/new.txt", 0, []byte("x"), true); err == nil {
		t.Error("wrote through a symlink that leaves the root")
	}
	if _, err := os.Stat(filepath.Join(base, "new.txt")); err == nil {
		t.Error("a file appeared outside the root")
	}
	// removing a link removes the link, never its target
	if err := f.Remove("out"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "secret.cfg")); err != nil {
		t.Error("the symlink target outside the root was touched")
	}
	e, err := f.Stat("link.cfg")
	if err != nil || e.Type != "l" {
		t.Errorf("a symlink must show as one: %+v %v", e, err)
	}
	if err := f.PutChunk("link.cfg", 0, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "secret.cfg")); string(b) != "secret" {
		t.Errorf("an upload over a symlink must replace the link, not write through it: %q", b)
	}
}

func TestChunkedUploadAndDownloadRoundTrip(t *testing.T) {
	f, base := setup(t)
	data := bytes.Repeat([]byte("abcdefghij"), 120_000) // 1.2 MB: three chunks
	for off := 0; off < len(data); off += MaxChunk {
		end := min(off+MaxChunk, len(data))
		if err := f.PutChunk("big.bin", int64(off), data[off:end], end == len(data)); err != nil {
			t.Fatalf("chunk at %d: %v", off, err)
		}
		if end < len(data) {
			if _, err := os.Stat(filepath.Join(base, "srv", "big.bin")); err == nil {
				t.Fatal("the file must not appear before the last chunk")
			}
		}
	}
	got, _ := os.ReadFile(filepath.Join(base, "srv", "big.bin"))
	if !bytes.Equal(got, data) {
		t.Fatal("uploaded content differs")
	}
	if _, err := os.Stat(filepath.Join(base, "srv", "big.bin"+UploadTmp)); err == nil {
		t.Error("the temporary file must be gone")
	}
	var back []byte
	for off := int64(0); ; {
		part, size, err := f.ReadAt("big.bin", off, MaxChunk)
		if err != nil || size != int64(len(data)) {
			t.Fatal(err, size)
		}
		back = append(back, part...)
		off += int64(len(part))
		if len(part) == 0 || off >= size {
			break
		}
	}
	if !bytes.Equal(back, data) {
		t.Fatal("downloaded content differs")
	}
}

func TestUploadChunksMustArriveInOrderAndKeepTheOldFile(t *testing.T) {
	f, base := setup(t)
	if err := f.PutChunk("server.cfg", 0, []byte("half"), false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "srv", "server.cfg")); string(b) != "ok" {
		t.Errorf("an unfinished upload must not touch the old file: %q", b)
	}
	if err := f.PutChunk("server.cfg", 99, []byte("x"), true); err == nil {
		t.Error("a chunk at the wrong offset must be refused")
	}
	if err := f.PutChunk("nodir/x.bin", 0, []byte("x"), true); err == nil {
		t.Error("the parent directory must exist")
	}
	if err := f.PutChunk("zero.bin", 0, nil, true); err != nil {
		t.Errorf("an empty file: %v", err)
	}
	if err := f.PutChunk("big", 0, make([]byte, MaxChunk+1), true); !errors.Is(err, ErrDenied) {
		t.Errorf("an oversized chunk: %v", err)
	}
}

func TestMkdirMoveRemove(t *testing.T) {
	f, base := setup(t)
	srv := filepath.Join(base, "srv")
	if err := f.Mkdir("plugins"); err != nil {
		t.Fatal(err)
	}
	if err := f.Mkdir("plugins"); err == nil {
		t.Error("mkdir over an existing name")
	}
	f.PutChunk("plugins/a.jar", 0, []byte("jar"), true)
	if err := f.Move("plugins", "mods"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv, "mods", "a.jar")); err != nil {
		t.Error("the directory did not move with its content")
	}
	if err := f.Move("mods", "server.cfg"); err == nil {
		t.Error("move must never overwrite")
	}
	if err := f.Move("mods", "mods/inner"); !errors.Is(err, ErrDenied) {
		t.Errorf("a directory into itself: %v", err)
	}
	if err := f.Remove("mods"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv, "mods")); err == nil {
		t.Error("the tree is still there")
	}
	if err := f.Remove("missing"); err == nil {
		t.Error("removing nothing must fail")
	}
}

func TestPutStreamVerifiesBeforeReplacing(t *testing.T) {
	f, base := setup(t)
	bad := errors.New("hash mismatch")
	if err := f.Put("server.cfg", strings.NewReader("evil"), 100, func() error { return bad }); !errors.Is(err, bad) {
		t.Fatalf("want the verify error, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "srv", "server.cfg")); string(b) != "ok" {
		t.Errorf("a failed verification must keep the old file: %q", b)
	}
	if err := f.Put("big", strings.NewReader("0123456789"), 5, nil); err == nil {
		t.Error("the size cap must apply")
	}
	if _, err := os.Stat(filepath.Join(base, "srv", "big"+UploadTmp)); err == nil {
		t.Error("the temporary file of a failed put must be removed")
	}
	if err := f.Put("good", strings.NewReader("fine"), 100, nil); err != nil {
		t.Fatal(err)
	}
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }
