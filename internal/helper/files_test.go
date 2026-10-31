package helper

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stokehold-gsm/stokehold/internal/safefs"
)

type fakeFiles struct {
	listed  []string
	read    string
	written []string // user|path|content
	err     error
}

func (f *fakeFiles) List(user string) ([]string, error) { return f.listed, f.err }
func (f *fakeFiles) Read(user, rel string) (string, error) {
	return f.read, f.err
}
func (f *fakeFiles) Write(user, rel, content string) error {
	f.written = append(f.written, user+"|"+rel+"|"+content)
	return f.err
}

func TestFileOpsRoundTripThroughTheBackend(t *testing.T) {
	ff := &fakeFiles{listed: []string{"serverfiles/server.properties"}, read: "motd=hi\n"}
	b := Backend{Files: ff}
	if resp := HandleFull(Request{Op: "files", User: "gs_a"}, b); !resp.OK || resp.Out != "serverfiles/server.properties" {
		t.Errorf("files: %+v", resp)
	}
	if resp := HandleFull(Request{Op: "read-file", User: "gs_a", Path: "serverfiles/server.properties"}, b); !resp.OK || resp.Out != "motd=hi\n" {
		t.Errorf("read: %+v", resp)
	}
	if resp := HandleFull(Request{Op: "write-file", User: "gs_a", Path: "lgsm/config-lgsm/mcserver/mcserver.cfg", Content: "x=1\n"}, b); !resp.OK {
		t.Errorf("write: %+v", resp)
	}
	if len(ff.written) != 1 || ff.written[0] != "gs_a|lgsm/config-lgsm/mcserver/mcserver.cfg|x=1\n" {
		t.Errorf("write got %v", ff.written)
	}
	ff.err = errors.New("boom")
	if resp := HandleFull(Request{Op: "write-file", User: "gs_a", Path: "serverfiles/a.cfg", Content: "x"}, b); resp.OK || resp.Err != "boom" {
		t.Errorf("an error must come back: %+v", resp)
	}
	if resp := HandleFull(Request{Op: "files", User: "gs_a"}, Backend{}); resp.OK || !strings.Contains(resp.Err, "not available") {
		t.Errorf("no backend: %+v", resp)
	}
}

func TestFileRequestValidation(t *testing.T) {
	ff := &fakeFiles{read: "x"}
	b := Backend{Files: ff, Adopted: only("factorio")}
	bad := []Request{
		{Op: "read-file", User: "gs_a", Path: ""},
		{Op: "read-file", User: "gs_a", Path: "serverfiles"},
		{Op: "read-file", User: "gs_a", Path: "serverfiles/"},
		{Op: "read-file", User: "gs_a", Path: "serverfiles/../.ssh/authorized_keys.conf"},
		{Op: "read-file", User: "gs_a", Path: "../etc/passwd.conf"},
		{Op: "read-file", User: "gs_a", Path: "/etc/passwd"},
		{Op: "read-file", User: "gs_a", Path: ".ssh/config.conf"},
		{Op: "read-file", User: "gs_a", Path: "serverfiles-evil/x.cfg"},
		{Op: "read-file", User: "gs_a", Path: "serverfiles/run.sh"},
		{Op: "read-file", User: "gs_a", Path: "serverfiles/binary.so"},
		{Op: "read-file", User: "gs_a", Path: `serverfiles\x.cfg`},
		{Op: "read-file", User: "gs_a", Path: "serverfiles/x\x00.cfg"},
		{Op: "read-file", User: "gs_a", Path: "serverfiles/" + strings.Repeat("a", 300) + ".cfg"},
		{Op: "write-file", User: "gs_a", Path: "serverfiles/a.cfg", Content: strings.Repeat("a", safefs.MaxEditSize+1)},
		{Op: "write-file", User: "root", Path: "serverfiles/a.cfg", Content: "x"},
		{Op: "write-file", User: "someone", Path: "serverfiles/a.cfg", Content: "x"},
		{Op: "read-file", User: "root", Path: "serverfiles/a.cfg"},
		{Op: "files", User: "someone"},
		{Op: "files", User: "-c"},
	}
	for _, r := range bad {
		if resp := HandleFull(r, b); resp.OK || !strings.Contains(resp.Err, "invalid request") {
			t.Errorf("%+v: %+v", r, resp)
		}
	}
	if len(ff.written) != 0 {
		t.Errorf("a refused request reached the file backend: %v", ff.written)
	}
	// valid ones for gs_ and for an adopted account
	for _, r := range []Request{
		{Op: "read-file", User: "gs_a", Path: "serverfiles/a/b.properties"},
		{Op: "write-file", User: "factorio", Path: "serverfiles/config/server-settings.json", Content: "{}"},
		{Op: "files", User: "factorio"},
	} {
		if resp := HandleFull(r, b); !resp.OK {
			t.Errorf("%+v: %+v", r, resp)
		}
	}
}

func TestOSFilesWorkInsideAHomeAndNowhereElse(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"serverfiles/config", "lgsm/config-lgsm/mcserver", ".ssh"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	os.WriteFile(filepath.Join(home, "serverfiles/server.properties"), []byte("motd=hi\n"), 0o640)
	os.WriteFile(filepath.Join(home, "serverfiles/binary.cfg"), []byte("a\x00b"), 0o640)
	os.WriteFile(filepath.Join(home, ".ssh/id.conf"), []byte("private"), 0o600)
	o := OSFiles{Home: func(string) (string, error) { return home, nil }}

	list, err := o.List("gs_a")
	if err != nil || strings.Join(list, ",") != "serverfiles/binary.cfg,serverfiles/server.properties" {
		t.Fatalf("list: %v %v", list, err)
	}
	if s, err := o.Read("gs_a", "serverfiles/server.properties"); err != nil || s != "motd=hi\n" {
		t.Errorf("read: %q %v", s, err)
	}
	if _, err := o.Read("gs_a", "serverfiles/binary.cfg"); err == nil {
		t.Error("a binary file was read as text")
	}
	if _, err := o.Read("gs_a", ".ssh/id.conf"); err == nil {
		t.Error("read outside the allowed trees")
	}
	if err := o.Write("gs_a", "serverfiles/server.properties", "motd=changed\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "serverfiles/server.properties")); string(b) != "motd=changed\n" {
		t.Errorf("not written: %q", b)
	}
	if err := o.Write("gs_a", "serverfiles/new.cfg", "x"); err == nil {
		t.Error("created a new file")
	}
	if err := o.Write("gs_a", ".ssh/id.conf", "x"); err == nil {
		t.Error("wrote outside the allowed trees")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".ssh/id.conf")); string(b) != "private" {
		t.Error("a protected file changed")
	}
	bad := OSFiles{Home: func(string) (string, error) { return "", errors.New("no such user") }}
	if _, err := bad.List("nobody"); err == nil {
		t.Error("an unknown user must fail")
	}
}

func TestInTree(t *testing.T) {
	for p, want := range map[string]bool{
		"serverfiles/a.cfg": true, "lgsm/config-lgsm/x/y.cfg": true,
		"serverfiles": false, "serverfiles/": false, "lgsm/config-lgsm": false, "lgsm/data/x.cfg": false,
		"serverfilesx/a.cfg": false, "": false, "a/serverfiles/b.cfg": false,
	} {
		if inTree(p) != want {
			t.Errorf("%q: want %v", p, want)
		}
	}
}

func TestDefaultCfgHidden(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "lgsm/config-lgsm/mcserver"), 0o755)
	for _, n := range []string{"_default.cfg", "mcserver.cfg", "common.cfg"} {
		os.WriteFile(filepath.Join(home, "lgsm/config-lgsm/mcserver", n), []byte("x=1\n"), 0o644)
	}
	o := OSFiles{Home: func(string) (string, error) { return home, nil }}
	list, _ := o.List("gs_a")
	for _, p := range list {
		if strings.HasSuffix(p, "_default.cfg") {
			t.Errorf("listed %s", p)
		}
	}
	if len(list) != 2 {
		t.Errorf("list: %v", list)
	}
	if err := o.Write("gs_a", "lgsm/config-lgsm/mcserver/_default.cfg", "y=2\n"); err == nil {
		t.Error("wrote the default config")
	}
	if err := Validate(Request{Op: "write-file", User: "gs_a", Path: "lgsm/config-lgsm/mcserver/_default.cfg", Content: "x"}); err == nil {
		t.Error("the request validation allows _default.cfg")
	}
}
