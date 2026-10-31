package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/safefs"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

// memFM is a game user's home in memory, answering the file manager ops.
type memFM struct {
	mu        sync.Mutex
	files     map[string][]byte
	dirs      map[string]bool
	calls     []helper.Request
	down      error
	failPutAt int64 // a put at this offset fails (0: never)
}

func newMemFM() *memFM { return &memFM{files: map[string][]byte{}, dirs: map[string]bool{".": true}} }

func (m *memFM) call(r helper.Request) (helper.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, r)
	if m.down != nil {
		return helper.Response{}, m.down
	}
	fail := func(msg string) (helper.Response, error) { return helper.Response{Err: msg}, nil }
	switch r.Op {
	case "files":
		return helper.Response{OK: true, Out: "serverfiles/server.properties"}, nil
	case "list-dir":
		if !m.dirs[r.Path] {
			return fail("no such directory")
		}
		var list []safefs.Entry
		for d := range m.dirs {
			if d != "." && parent(d) == r.Path {
				list = append(list, safefs.Entry{Name: base0(d), Type: "d", Mtime: 1700000000})
			}
		}
		for f, b := range m.files {
			if parent(f) == r.Path {
				list = append(list, safefs.Entry{Name: base0(f), Type: "f", Size: int64(len(b)), Mtime: 1700000000})
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		b, _ := json.Marshal(map[string]any{"list": list, "more": false})
		return helper.Response{OK: true, Out: string(b)}, nil
	case "stat":
		if b, ok := m.files[r.Path]; ok {
			j, _ := json.Marshal(safefs.Entry{Name: base0(r.Path), Type: "f", Size: int64(len(b))})
			return helper.Response{OK: true, Out: string(j)}, nil
		}
		if m.dirs[r.Path] {
			j, _ := json.Marshal(safefs.Entry{Name: base0(r.Path), Type: "d"})
			return helper.Response{OK: true, Out: string(j)}, nil
		}
		return fail("not found")
	case "mkdir":
		if m.dirs[r.Path] || m.files[r.Path] != nil {
			return fail("exists")
		}
		m.dirs[r.Path] = true
	case "put":
		if m.failPutAt != 0 && r.Offset == m.failPutAt {
			return fail("boom")
		}
		data, _ := base64.StdEncoding.DecodeString(r.Data)
		tmp := r.Path + safefs.UploadTmp
		if r.Offset == 0 {
			m.files[tmp] = nil
		}
		if int64(len(m.files[tmp])) != r.Offset {
			return fail("out of order")
		}
		m.files[tmp] = append(m.files[tmp], data...)
		if r.Final {
			m.files[r.Path] = append([]byte{}, m.files[tmp]...)
			delete(m.files, tmp)
		}
	case "get":
		b, ok := m.files[r.Path]
		if !ok {
			return fail("not found")
		}
		end := min(r.Offset+r.Length, int64(len(b)))
		return helper.Response{OK: true, Out: base64.StdEncoding.EncodeToString(b[min(r.Offset, end):end])}, nil
	case "delete":
		found := false
		for f := range m.files {
			if f == r.Path || strings.HasPrefix(f, r.Path+"/") {
				delete(m.files, f)
				found = true
			}
		}
		for d := range m.dirs {
			if d == r.Path || strings.HasPrefix(d, r.Path+"/") {
				delete(m.dirs, d)
				found = true
			}
		}
		if !found {
			return fail("not found")
		}
	case "rename":
		if b, ok := m.files[r.Path]; ok {
			if _, taken := m.files[r.To]; taken {
				return fail("exists")
			}
			m.files[r.To] = b
			delete(m.files, r.Path)
		} else if m.dirs[r.Path] {
			m.dirs[r.To] = true
			delete(m.dirs, r.Path)
		} else {
			return fail("not found")
		}
	}
	return helper.Response{OK: true}, nil
}

func parent(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}

func base0(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

func (m *memFM) ops(op string) []helper.Request {
	var out []helper.Request
	for _, c := range m.calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func fmRig(t *testing.T) (*rig, *memFM, string, string) {
	t.Helper()
	r := newRig(t)
	m := newMemFM()
	r.s.Helper = m.call
	r.setup()
	r.login("")
	id := itoaTest(r.addServerRow())
	return r, m, id, r.csrf()
}

func (r *rig) upload(id, tok, dir string, csrfFirst bool, files map[string][]byte) *httptest.ResponseRecorder {
	r.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if csrfFirst {
		mw.WriteField("csrf", tok)
	}
	mw.WriteField("path", dir)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, _ := mw.CreateFormFile("file", n)
		w.Write(files[n])
	}
	if !csrfFirst {
		mw.WriteField("csrf", tok)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/servers/"+id+"/fm/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "203.0.113.7:5555"
	for _, c := range r.jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func TestBrowseListsFoldersFirstAndSorts(t *testing.T) {
	r, m, id, _ := fmRig(t)
	m.dirs["plugins"] = true
	m.dirs["serverfiles"] = true
	m.files["a.txt"] = []byte("12345")
	m.files["z.jar"] = bytes.Repeat([]byte("x"), 2000)
	m.files["serverfiles/server.properties"] = []byte("motd=hi")
	m.files["serverfiles/world.dat"] = []byte("bin")

	body := r.do("GET", "/servers/"+id+"/files", nil, nil).Body.String()
	if i, j := strings.Index(body, "plugins"), strings.Index(body, "a.txt"); i < 0 || j < 0 || i > j {
		t.Error("folders must come before files")
	}
	if !strings.Contains(body, "2.0 KB") || !strings.Contains(body, "/fm/download?path=z.jar") {
		t.Errorf("size or download link missing:\n%s", body)
	}
	if strings.Contains(body, "/edit?path=z.jar") || strings.Contains(body, "/edit?path=a.txt") {
		t.Error("only configs inside the allowed trees get an Edit link")
	}
	if !strings.Contains(body, "server.properties") {
		t.Error("the quick list of configs is gone")
	}
	desc := r.do("GET", "/servers/"+id+"/files?sort=size&order=desc", nil, nil).Body.String()
	if strings.Index(desc, "z.jar") > strings.Index(desc, "a.txt") {
		t.Error("sorting by size, biggest first")
	}
	inner := r.do("GET", "/servers/"+id+"/files?path=serverfiles", nil, nil).Body.String()
	if !strings.Contains(inner, "/edit?path=serverfiles%2Fserver.properties") || strings.Contains(inner, "/edit?path=serverfiles%2Fworld.dat") {
		t.Errorf("edit link rules inside a folder:\n%s", inner)
	}
	if !strings.Contains(inner, `href="/servers/`+id+`/files">/</a>`) || !strings.Contains(inner, ">serverfiles</a>") {
		t.Error("breadcrumbs")
	}
	if rec := r.do("GET", "/servers/"+id+"/files?path=nope", nil, nil); rec.Code != 404 || !strings.Contains(rec.Body.String(), "No such folder") {
		t.Errorf("a missing folder: %d", rec.Code)
	}
}

func TestCreateRenameDeleteWithConfirmation(t *testing.T) {
	r, m, id, tok := fmRig(t)
	post := func(tail string, f url.Values) *httptest.ResponseRecorder {
		f.Set("csrf", tok)
		return r.do("POST", "/servers/"+id+tail, f, nil)
	}
	if rec := post("/fm/mkdir", url.Values{"path": {"."}, "name": {"plugins"}}); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=mkdir") {
		t.Fatalf("mkdir: %d", rec.Code)
	}
	if !m.dirs["plugins"] {
		t.Fatal("the folder was not created")
	}
	if rec := post("/fm/touch", url.Values{"path": {"plugins"}, "name": {"a.yml"}}); rec.Code != 303 {
		t.Fatalf("touch: %d", rec.Code)
	}
	if _, ok := m.files["plugins/a.yml"]; !ok {
		t.Fatal("the empty file was not created")
	}
	if rec := post("/fm/touch", url.Values{"path": {"plugins"}, "name": {"a.yml"}}); rec.Code != 409 || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("creating over an existing file: %d", rec.Code)
	}
	if rec := post("/fm/rename", url.Values{"path": {"plugins/a.yml"}, "to": {"plugins/b.yml"}}); rec.Code != 303 {
		t.Fatalf("rename: %d", rec.Code)
	}
	if _, ok := m.files["plugins/b.yml"]; !ok {
		t.Fatal("not renamed")
	}
	if form := r.do("GET", "/servers/"+id+"/fm/rename?path=plugins/b.yml", nil, nil); form.Code != 200 || !strings.Contains(form.Body.String(), `value="plugins/b.yml"`) {
		t.Errorf("rename form: %d", form.Code)
	}

	// delete: the first POST only asks, the second one does it
	del := url.Values{"paths": {"plugins/b.yml"}, "dir": {"plugins"}}
	if rec := post("/fm/delete", del); rec.Code != 200 || !strings.Contains(rec.Body.String(), "plugins/b.yml") || len(m.ops("delete")) != 0 {
		t.Fatalf("the confirmation step: %d, deletes %d", rec.Code, len(m.ops("delete")))
	}
	del.Set("confirmed", "1")
	if rec := post("/fm/delete", del); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=delete") {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, ok := m.files["plugins/b.yml"]; ok {
		t.Fatal("still there")
	}
	for _, a := range []string{"file.mkdir", "file.touch", "file.rename", "file.delete"} {
		if r.st.AuditCount(a) != 1 {
			t.Errorf("%s was not audited exactly once", a)
		}
	}
	// a bulk delete takes several paths in one confirmed request
	m.files["x1"], m.files["x2"] = []byte("1"), []byte("2")
	post("/fm/delete", url.Values{"paths": {"x1", "x2"}, "dir": {"."}, "confirmed": {"1"}})
	if len(m.files) != 0 {
		t.Errorf("bulk delete left %v", m.files)
	}
}

func TestUploadStreamsInChunksAndChecksCSRFFirst(t *testing.T) {
	r, m, id, tok := fmRig(t)
	m.dirs["mods"] = true
	data := bytes.Repeat([]byte("0123456789abcdef"), 70_000) // 1.12 MB, three chunks
	rec := r.upload(id, tok, "mods", true, map[string][]byte{"big.jar": data, "empty.txt": nil})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=upload") {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	if !bytes.Equal(m.files["mods/big.jar"], data) {
		t.Fatal("content differs")
	}
	if f, ok := m.files["mods/empty.txt"]; !ok || len(f) != 0 {
		t.Error("an empty file must be created too")
	}
	var offs []int64
	finals := 0
	for _, c := range m.ops("put") {
		if c.Path == "mods/big.jar" {
			offs = append(offs, c.Offset)
			if c.Final {
				finals++
			}
		}
	}
	if len(offs) != 3 || offs[0] != 0 || offs[1] != safefs.MaxChunk || finals != 1 {
		t.Errorf("chunks: offsets %v, %d final", offs, finals)
	}
	if r.st.AuditCount("file.upload") != 2 {
		t.Error("each uploaded file must be audited")
	}

	m.calls = nil
	if rec := r.upload(id, "wrong", "mods", true, map[string][]byte{"evil.jar": []byte("x")}); rec.Code != 403 || len(m.ops("put")) != 0 {
		t.Errorf("a bad CSRF token: %d, %d puts", rec.Code, len(m.ops("put")))
	}
	if rec := r.upload(id, tok, "mods", false, map[string][]byte{"late.jar": []byte("x")}); rec.Code != 403 || len(m.ops("put")) != 0 {
		t.Errorf("the token after the files: %d, %d puts", rec.Code, len(m.ops("put")))
	}
	if rec := r.upload(id, tok, "mods", true, map[string][]byte{"../../evil.jar": []byte("x")}); rec.Code != 303 || m.files["mods/evil.jar"] == nil || len(m.ops("put")) != 1 {
		t.Errorf("a path in the file name must be reduced to its base name: %d", rec.Code)
	}
	if rec := r.upload(id, tok, "mods", true, nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "No file chosen") {
		t.Errorf("no file: %d", rec.Code)
	}
}

func TestUploadFailureRemovesTheTemporaryFile(t *testing.T) {
	r, m, id, tok := fmRig(t)
	m.failPutAt = safefs.MaxChunk // the second chunk is refused
	data := bytes.Repeat([]byte("z"), 2*safefs.MaxChunk+10)
	rec := r.upload(id, tok, ".", true, map[string][]byte{"a.bin": data})
	if rec.Code != 400 {
		t.Errorf("a failed upload must show an error, got %d", rec.Code)
	}
	if _, ok := m.files["a.bin"]; ok {
		t.Error("a half-sent file must not appear")
	}
	var cleaned bool
	for _, c := range m.ops("delete") {
		cleaned = cleaned || c.Path == "a.bin"+safefs.UploadTmp
	}
	if !cleaned {
		t.Error("the temporary file must be removed after a failure")
	}
}

func TestDownloadStreamsWithSafeHeaders(t *testing.T) {
	r, m, id, _ := fmRig(t)
	data := bytes.Repeat([]byte("jar!"), 300_000) // 1.2 MB
	m.files["server ü.jar"] = data
	rec := r.do("GET", "/servers/"+id+"/fm/download?path="+url.QueryEscape("server ü.jar"), nil, nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("download: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Type") != "application/octet-stream" || h.Get("Content-Length") != "1200000" {
		t.Errorf("headers: %v", h)
	}
	if r.st.AuditCount("file.download") != 1 {
		t.Error("downloads must be audited")
	}
	for _, p := range []string{"missing.jar", "..%2Fetc%2Fpasswd", "", "plugins"} {
		m.dirs["plugins"] = true
		if rec := r.do("GET", "/servers/"+id+"/fm/download?path="+p, nil, nil); rec.Code != 404 {
			t.Errorf("download %q: %d", p, rec.Code)
		}
	}
}

func TestFileManagerIsForOwnersAndAdminsOnly(t *testing.T) {
	r, m, id, _ := fmRig(t)
	m.files["a.txt"] = []byte("x")
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	r.loginAs("viewer@example.com", pass)
	tok := r.csrf()
	m.calls = nil
	for _, c := range []struct{ method, path string }{
		{"GET", "/servers/" + id + "/files"}, {"GET", "/servers/" + id + "/fm/download?path=a.txt"},
		{"GET", "/servers/" + id + "/fm/rename?path=a.txt"}, {"GET", "/servers/" + id + "/fm/delete?path=a.txt"},
	} {
		if rec := r.do(c.method, c.path, nil, nil); rec.Code != 403 {
			t.Errorf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	for _, tail := range []string{"/fm/mkdir", "/fm/touch", "/fm/rename", "/fm/delete"} {
		f := url.Values{"csrf": {tok}, "path": {"a.txt"}, "paths": {"a.txt"}, "name": {"x"}, "to": {"b"}, "confirmed": {"1"}}
		if rec := r.do("POST", "/servers/"+id+tail, f, nil); rec.Code != 403 {
			t.Errorf("viewer POST %s: %d", tail, rec.Code)
		}
	}
	if rec := r.upload(id, tok, ".", true, map[string][]byte{"x": []byte("1")}); rec.Code != 403 {
		t.Errorf("viewer upload: %d", rec.Code)
	}
	if len(m.calls) != 0 || len(m.files) != 1 {
		t.Errorf("a refused request reached the helper: %+v", m.calls)
	}
}

func TestFileManagerRejectsBadPathsBeforeTheHelper(t *testing.T) {
	r, m, id, tok := fmRig(t)
	m.calls = nil
	post := func(tail string, f url.Values) int {
		f.Set("csrf", tok)
		return r.do("POST", "/servers/"+id+tail, f, nil).Code
	}
	for _, name := range []string{"a/b", "..", ".", "", "a\\b", "x\x00y", strings.Repeat("n", 201), "line\nbreak"} {
		if c := post("/fm/mkdir", url.Values{"path": {"."}, "name": {name}}); c != 400 {
			t.Errorf("mkdir name %q: %d", name, c)
		}
	}
	if c := post("/fm/mkdir", url.Values{"path": {"../x"}, "name": {"ok"}}); c != 400 {
		t.Errorf("mkdir in a parent: %d", c)
	}
	if c := post("/fm/delete", url.Values{"paths": {"."}, "dir": {"."}, "confirmed": {"1"}}); c != 400 {
		t.Errorf("deleting the home: %d", c)
	}
	if c := post("/fm/delete", url.Values{"paths": {"/etc"}, "confirmed": {"1"}}); c != 400 {
		t.Errorf("deleting an absolute path: %d", c)
	}
	if c := post("/fm/delete", url.Values{"dir": {"."}, "confirmed": {"1"}}); c != 400 {
		t.Errorf("deleting nothing: %d", c)
	}
	if c := post("/fm/rename", url.Values{"path": {"a"}, "to": {"../b"}}); c != 400 {
		t.Errorf("renaming out of the folder: %d", c)
	}
	many := make([]string, 201)
	for i := range many {
		many[i] = "f" + itoaTest(int64(i))
	}
	if c := post("/fm/delete", url.Values{"paths": many, "dir": {"."}, "confirmed": {"1"}}); c != 400 {
		t.Errorf("too many paths: %d", c)
	}
	// a hostile folder in the address bar falls back to the home
	r.do("GET", "/servers/"+id+"/files?path=../../etc", nil, nil)
	for _, c := range m.calls {
		if strings.Contains(c.Path, "..") || strings.HasPrefix(c.Path, "/") {
			t.Errorf("a hostile path reached the helper: %+v", c)
		}
	}
	if post("/fm/mkdir", url.Values{"path": {"."}, "name": {"fine"}}) != 303 {
		t.Error("a normal name must still work")
	}
	if r.do("POST", "/servers/"+id+"/fm/mkdir", url.Values{"path": {"."}, "name": {"x"}}, nil).Code != 403 {
		t.Error("a post without the CSRF token must be refused")
	}
}

func TestFileManagerReportsHelperProblems(t *testing.T) {
	r, m, id, tok := fmRig(t)
	m.down = errors.New("socket closed")
	if rec := r.do("GET", "/servers/"+id+"/files", nil, nil); rec.Code != 502 {
		t.Errorf("a down helper: %d", rec.Code)
	}
	if rec := r.do("POST", "/servers/"+id+"/fm/mkdir", url.Values{"csrf": {tok}, "path": {"."}, "name": {"a"}}, nil); rec.Code != 502 {
		t.Errorf("mkdir with a down helper: %d", rec.Code)
	}
	m.down = nil
	if rec := r.do("GET", "/servers/99/files", nil, nil); rec.Code != 404 {
		t.Errorf("unknown server: %d", rec.Code)
	}
	m.dirs["d"] = true
	if rec := r.do("POST", "/servers/"+id+"/fm/mkdir", url.Values{"csrf": {tok}, "path": {"."}, "name": {"d"}}, nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "did not work") {
		t.Errorf("a refusal from the helper: %d", rec.Code)
	}
}
