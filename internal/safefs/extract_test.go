package safefs

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type ent struct {
	name, body string
	mode       fs.FileMode
}

func zipOf(t *testing.T, entries []ent) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(e.body))
	}
	w.Close()
	return b.Bytes()
}

func tgzOf(t *testing.T, entries []ent) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.mode&fs.ModeSymlink != 0:
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, e.body, 0
		case strings.HasSuffix(e.name, "/"):
			h.Typeflag, h.Size = tar.TypeDir, 0
		case e.mode != 0:
			h.Mode = int64(e.mode.Perm())
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func rootWith(t *testing.T, name string, data []byte) (*FS, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, dir
}

func TestExtractZipWithSubfolderAndExecBit(t *testing.T) {
	f, dir := rootWith(t, "pack.zip", zipOf(t, []ent{
		{name: "Pack/"}, {name: "Pack/BepInEx/core/a.dll", body: "dll"}, {name: "Pack/run.sh", body: "#!", mode: 0o755},
		{name: "manifest.json", body: "{}"}, {name: "Pack\\win\\path.txt", body: "w"},
	}))
	n, err := f.Extract("pack.zip", "serverfiles", "Pack")
	if err != nil || n != 3 {
		t.Fatalf("extract: %d %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "serverfiles/BepInEx/core/a.dll")); string(b) != "dll" {
		t.Fatal("the file under the subfolder must land without the subfolder")
	}
	if _, err := os.Stat(filepath.Join(dir, "serverfiles/manifest.json")); err == nil {
		t.Fatal("entries outside the subfolder must be skipped")
	}
	if _, err := os.Stat(filepath.Join(dir, "serverfiles/win/path.txt")); err != nil {
		t.Fatal("backslash names from Windows archives must become folders")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "serverfiles/run.sh")); fi.Mode().Perm()&0o100 == 0 {
			t.Error("an executable must stay executable")
		}
	}
}

func TestExtractRefusesZipSlipAndAbsoluteNames(t *testing.T) {
	for _, bad := range []string{"../evil.txt", "a/../../evil.txt", "/etc/evil.txt", "ok/../../x"} {
		f, dir := rootWith(t, "x.zip", zipOf(t, []ent{{name: "fine.txt", body: "1"}, {name: bad, body: "pwn"}}))
		if _, err := f.Extract("x.zip", "out", ""); err == nil {
			t.Errorf("%q must be refused", bad)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
			t.Fatalf("%q escaped the root", bad)
		}
	}
	f, _ := rootWith(t, "x.tar.gz", tgzOf(t, []ent{{name: "../../evil", body: "pwn"}}))
	if _, err := f.Extract("x.tar.gz", "out", ""); err == nil {
		t.Error("a tar entry leaving the target must be refused")
	}
}

func TestExtractSkipsSymlinksAndNeverWritesThroughThem(t *testing.T) {
	outside := t.TempDir()
	f, dir := rootWith(t, "x.tgz", tgzOf(t, []ent{
		{name: "link", body: outside, mode: fs.ModeSymlink},
		{name: "plugins/", body: ""}, {name: "plugins/a.cs", body: "code"},
	}))
	n, err := f.Extract("x.tgz", "out", "")
	if err != nil || n != 1 {
		t.Fatalf("extract: %d %v", n, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "out/link")); err == nil {
		t.Fatal("a symlink entry must not be created")
	}
	// a symlink planted in the target folder before must not be followed
	if runtime.GOOS != "windows" {
		os.MkdirAll(filepath.Join(dir, "t2"), 0o755)
		os.Symlink(outside, filepath.Join(dir, "t2/plugins"))
		if _, err := f.Extract("x.tgz", "t2", ""); err == nil {
			t.Fatal("extracting into a planted symlinked folder must be refused")
		}
		if ents, _ := os.ReadDir(outside); len(ents) != 0 {
			t.Fatal("a file was written through a planted symlink")
		}
	}
}

func TestExtractRefusesSensitiveNamesAndOddArchives(t *testing.T) {
	f, _ := rootWith(t, "x.zip", zipOf(t, []ent{{name: ".ssh/authorized_keys", body: "key"}}))
	if _, err := f.Extract("x.zip", "out", ""); err == nil {
		t.Error("an archive may not drop SSH keys")
	}
	g, _ := rootWith(t, "x.rar", []byte("nope"))
	if _, err := g.Extract("x.rar", "out", ""); err == nil {
		t.Error("only zip and tar.gz are accepted")
	}
	if _, err := g.Extract("x.rar", "../out", ""); err == nil {
		t.Error("the target must stay inside the root")
	}
}
