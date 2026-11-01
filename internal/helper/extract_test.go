package helper

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractOpUnpacksInsideTheHomeAndValidates(t *testing.T) {
	b, home := fmBackend(t)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, _ := w.Create("Oxide/RustDedicated_Data/Managed/Oxide.Rust.dll")
	fw.Write([]byte("dll"))
	w.Close()
	os.WriteFile(filepath.Join(home, "oxide.zip"), buf.Bytes(), 0o644)
	r := call(b, Request{Op: "extract", Path: "oxide.zip", To: "serverfiles", Sub: "Oxide"})
	if !r.OK || r.Out != "1" {
		t.Fatalf("extract: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, "serverfiles/RustDedicated_Data/Managed/Oxide.Rust.dll")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Request{
		{Op: "extract", Path: "../oxide.zip", To: "serverfiles"},
		{Op: "extract", Path: "oxide.zip", To: "/etc"},
		{Op: "extract", Path: "oxide.zip", To: "serverfiles", Sub: "../x"},
		{Op: "extract", Path: "oxide.zip", To: "."},
	} {
		if r := call(b, bad); r.OK {
			t.Errorf("must be refused: %+v", bad)
		}
	}
}
