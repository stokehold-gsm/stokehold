package web

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"stokehold/internal/helper"
	"stokehold/internal/safefs"
	"stokehold/internal/store"
)

// fileHelper answers the three file operations.
type fileHelper struct {
	calls   []helper.Request
	listing string
	content string
	fail    bool
	down    error
}

func (f *fileHelper) call(r helper.Request) (helper.Response, error) {
	f.calls = append(f.calls, r)
	if f.down != nil {
		return helper.Response{}, f.down
	}
	if f.fail {
		return helper.Response{OK: false, Err: "path not allowed"}, nil
	}
	switch r.Op {
	case "files":
		return helper.Response{OK: true, Out: f.listing}, nil
	case "read-file":
		return helper.Response{OK: true, Out: f.content}, nil
	}
	return helper.Response{OK: true}, nil
}

func fileRig(t *testing.T) (*rig, *fileHelper, int64, string) {
	r := newRig(t)
	f := &fileHelper{listing: "serverfiles/server.properties\nlgsm/config-lgsm/mcserver/mcserver.cfg\n", content: "motd=<b>hi</b>\nrcon.password=secret\n"}
	r.s.Helper = f.call
	r.setup()
	r.login("")
	id := r.addServerRow()
	return r, f, id, r.csrf()
}

func TestFileListAndEditorShowTheConfig(t *testing.T) {
	r, f, id, _ := fileRig(t)
	list := r.do("GET", "/servers/"+itoaTest(id)+"/files", nil, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), "serverfiles/server.properties") || !strings.Contains(list.Body.String(), "mcserver.cfg") {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	if f.calls[0].Op != "files" || f.calls[0].User != "factorio" {
		t.Errorf("helper got %+v", f.calls[0])
	}
	page := r.do("GET", "/servers/"+itoaTest(id)+"/edit?path=serverfiles/server.properties", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "rcon.password=secret") {
		t.Fatalf("edit: %d", page.Code)
	}
	if strings.Contains(page.Body.String(), "<b>hi</b>") || !strings.Contains(page.Body.String(), "&lt;b&gt;hi&lt;/b&gt;") {
		t.Error("the file content must be HTML-escaped in the editor")
	}
	if f.calls[1].Op != "read-file" || f.calls[1].Path != "serverfiles/server.properties" {
		t.Errorf("read request: %+v", f.calls[1])
	}
}

func TestSavingSendsLFContentAndAuditsWithoutTheContent(t *testing.T) {
	r, f, id, tok := fileRig(t)
	form := url.Values{"csrf": {tok}, "path": {"serverfiles/server.properties"}, "content": {"motd=hi\r\nrcon.password=newsecret\r\n"}}
	rec := r.do("POST", "/servers/"+itoaTest(id)+"/edit", form, nil)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "saved=1") {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	last := f.calls[len(f.calls)-1]
	if last.Op != "write-file" || last.Path != "serverfiles/server.properties" || last.Content != "motd=hi\nrcon.password=newsecret\n" || last.User != "factorio" {
		t.Errorf("helper got %+v", last)
	}
	var detail string
	r.st.AuditCount("file.edit")
	if r.st.AuditCount("file.edit") != 1 {
		t.Fatal("not audited")
	}
	if rows, _ := r.st.AuditRows(5); len(rows) > 0 {
		for _, row := range rows {
			detail += row
		}
	}
	if strings.Contains(detail, "newsecret") || strings.Contains(detail, "rcon") {
		t.Errorf("the file content leaked into the audit log: %s", detail)
	}
	if page := r.do("GET", "/servers/"+itoaTest(id)+"/files?saved=1", nil, nil).Body.String(); !strings.Contains(page, "File saved") {
		t.Error("a saved file should be confirmed")
	}
}

func TestViewersCannotSeeOrChangeConfigs(t *testing.T) {
	r, f, id, _ := fileRig(t)
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	r.loginAs("viewer@example.com", pass)
	tok := r.csrf()
	f.calls = nil
	for _, c := range []struct{ method, path string }{
		{"GET", "/servers/" + itoaTest(id) + "/files"},
		{"GET", "/servers/" + itoaTest(id) + "/edit?path=serverfiles/server.properties"},
	} {
		if rec := r.do(c.method, c.path, nil, nil); rec.Code != 403 || strings.Contains(rec.Body.String(), "rcon") {
			t.Errorf("%s: %d", c.path, rec.Code)
		}
	}
	form := url.Values{"csrf": {tok}, "path": {"serverfiles/server.properties"}, "content": {"x"}}
	if rec := r.do("POST", "/servers/"+itoaTest(id)+"/edit", form, nil); rec.Code != 403 {
		t.Errorf("viewer save: %d", rec.Code)
	}
	if body := r.do("GET", "/", nil, nil).Body.String(); strings.Contains(body, "/files") {
		t.Error("the Files link must not be shown to a viewer")
	}
	if len(f.calls) != 0 {
		t.Errorf("a refused request reached the helper: %+v", f.calls)
	}
}

func TestFileEditorHandlesProblems(t *testing.T) {
	r, f, id, tok := fileRig(t)
	if rec := r.do("GET", "/servers/99/files", nil, nil); rec.Code != 404 {
		t.Errorf("unknown server: %d", rec.Code)
	}
	if rec := r.do("GET", "/servers/"+itoaTest(id)+"/edit", nil, nil); rec.Code != 404 {
		t.Errorf("no path: %d", rec.Code)
	}
	f.fail = true // the helper refuses, e.g. a path outside the allowed trees
	rec := r.do("GET", "/servers/"+itoaTest(id)+"/edit?path=.ssh/x.conf", nil, nil)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Could not read") {
		t.Errorf("refused read: %d", rec.Code)
	}
	form := url.Values{"csrf": {tok}, "path": {"../etc/x.cfg"}, "content": {"x"}}
	rec = r.do("POST", "/servers/"+itoaTest(id)+"/edit", form, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Could not read or save") || !strings.Contains(rec.Body.String(), "x") {
		t.Errorf("refused save: %d", rec.Code)
	}
	if r.st.AuditCount("file.edit") != 0 {
		t.Error("a failed save was audited as an edit")
	}
	f.fail, f.down = false, errors.New("connection refused")
	if rec := r.do("GET", "/servers/"+itoaTest(id)+"/files", nil, nil); !strings.Contains(rec.Body.String(), "not reachable") {
		t.Error("a missing helper must be reported")
	}
	f.down = nil
	big := strings.Repeat("a", safefs.MaxEditSize+1)
	rec = r.do("POST", "/servers/"+itoaTest(id)+"/edit", url.Values{"csrf": {tok}, "path": {"serverfiles/a.cfg"}, "content": {big}}, nil)
	if rec.Code != 413 {
		t.Errorf("oversized content: %d", rec.Code)
	}
	if rec := r.do("POST", "/servers/"+itoaTest(id)+"/edit", url.Values{"path": {"serverfiles/a.cfg"}, "content": {"x"}}, nil); rec.Code != 403 {
		t.Errorf("no csrf: %d", rec.Code)
	}
}

func itoaTest(n int64) string { return string(rune('0' + n)) }
