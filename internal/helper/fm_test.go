package helper

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stokehold-gsm/stokehold/internal/safefs"
)

func fmBackend(t *testing.T) (Backend, string) {
	t.Helper()
	home := t.TempDir()
	return Backend{Files: OSFiles{Home: func(string) (string, error) { return home, nil }}}, home
}

func call(b Backend, r Request) Response {
	r.User = "gs_a"
	return HandleFull(r, b)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestFileManagerOpsThroughTheHandler(t *testing.T) {
	b, home := fmBackend(t)
	if r := call(b, Request{Op: "mkdir", Path: "serverfiles"}); !r.OK {
		t.Fatalf("mkdir: %+v", r)
	}
	if r := call(b, Request{Op: "put", Path: "serverfiles/a.txt", Data: b64("hello "), Offset: 0}); !r.OK {
		t.Fatalf("put 1: %+v", r)
	}
	if r := call(b, Request{Op: "put", Path: "serverfiles/a.txt", Data: b64("world"), Offset: 6, Final: true}); !r.OK {
		t.Fatalf("put 2: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "serverfiles", "a.txt")); string(got) != "hello world" {
		t.Errorf("content %q", got)
	}
	r := call(b, Request{Op: "list-dir", Path: "serverfiles"})
	var out struct {
		List []safefs.Entry
		More bool
	}
	if err := json.Unmarshal([]byte(r.Out), &out); err != nil || len(out.List) != 1 || out.List[0].Name != "a.txt" || out.List[0].Size != 11 {
		t.Errorf("list: %+v %v", r, err)
	}
	if r := call(b, Request{Op: "list-dir", Path: "."}); !r.OK {
		t.Errorf("the home itself may be listed: %+v", r)
	}
	if r := call(b, Request{Op: "get", Path: "serverfiles/a.txt", Offset: 6, Length: 100}); !r.OK || r.Out != b64("world") {
		t.Errorf("get: %+v", r)
	}
	if r := call(b, Request{Op: "stat", Path: "serverfiles/a.txt"}); !r.OK || !strings.Contains(r.Out, `"s":11`) {
		t.Errorf("stat: %+v", r)
	}
	if r := call(b, Request{Op: "rename", Path: "serverfiles/a.txt", To: "serverfiles/b.txt"}); !r.OK {
		t.Errorf("rename: %+v", r)
	}
	if r := call(b, Request{Op: "delete", Path: "serverfiles"}); !r.OK {
		t.Errorf("delete: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, "serverfiles")); err == nil {
		t.Error("the directory survived")
	}
}

func TestFileManagerValidationAndConfinement(t *testing.T) {
	b, home := fmBackend(t)
	os.WriteFile(filepath.Join(filepath.Dir(home), "outside.txt"), []byte("x"), 0o644)
	for _, req := range []Request{
		{Op: "list-dir", Path: ".."},
		{Op: "list-dir", Path: ""},
		{Op: "stat", Path: "../outside.txt"},
		{Op: "get", Path: "/etc/passwd", Length: 10},
		{Op: "get", Path: "a/../../outside.txt", Length: 10},
		{Op: "get", Path: "f", Length: safefs.MaxChunk + 1},
		{Op: "put", Path: "a.txt", Data: strings.Repeat("A", 800_000)},
		{Op: "put", Path: "a.txt", Offset: -1},
		{Op: "mkdir", Path: "."},
		{Op: "delete", Path: "."},
		{Op: "delete", Path: "a//b"},
		{Op: "rename", Path: "a", To: "../b"},
		{Op: "rename", Path: "a", To: ""},
		{Op: "fetch", Path: "a.jar", URL: "http://api.papermc.io/x"},
		{Op: "fetch", Path: "a.jar", URL: "https://evil.example/x.jar"},
		{Op: "fetch", Path: "a.jar", URL: "https://user:pw@api.papermc.io/x"},
		{Op: "fetch", Path: "a.jar", URL: "https://api.papermc.io:8443/x"},
		{Op: "fetch", Path: "a.jar", URL: "https://api.papermc.io/x", SHA256: "nothex"},
		{Op: "http-get-json", URL: "https://127.0.0.1/x"},
		{Op: "mkdir", Path: "serverfiles/x", User: "root"},
	} {
		req := req
		if req.User == "" {
			req.User = "gs_a"
		}
		if resp := HandleFull(req, b); resp.OK || resp.Err == "" {
			t.Errorf("%+v must be refused: %+v", req, resp)
		}
	}
	// sensitive names are refused by the confinement layer
	for _, p := range []string{".ssh", ".ssh/authorized_keys", ".bashrc", "serverfiles/.profile"} {
		if r := call(b, Request{Op: "mkdir", Path: p}); r.OK {
			t.Errorf("mkdir %s must be refused", p)
		}
		if r := call(b, Request{Op: "put", Path: p, Data: b64("x"), Final: true}); r.OK {
			t.Errorf("put %s must be refused", p)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); err == nil {
		t.Error(".ssh was created")
	}
	// an adopted account may use the file manager, a stranger may not
	if r := HandleFull(Request{Op: "list-dir", User: "factorio", Path: "."}, Backend{Files: b.Files}); r.OK {
		t.Errorf("an unlisted account: %+v", r)
	}
	if r := HandleFull(Request{Op: "list-dir", User: "factorio", Path: "."}, Backend{Files: b.Files, Adopted: only("factorio")}); !r.OK {
		t.Errorf("an adopted account: %+v", r)
	}
	if r := HandleFull(Request{Op: "list-dir", Path: "."}, Backend{}); r.OK || !strings.Contains(r.Err, "user") {
		t.Errorf("a missing user: %+v", r)
	}
	if r := call(Backend{}, Request{Op: "list-dir", Path: "."}); r.OK || !strings.Contains(r.Err, "not available") {
		t.Errorf("no backend: %+v", r)
	}
}

// startTLS makes a stand-in for an allowed host and points the helper at it.
func startTLS(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	FetchHosts[u.Hostname()] = true
	oldT := fetchTransport
	fetchTransport = srv.Client().Transport
	t.Cleanup(func() { delete(FetchHosts, u.Hostname()); fetchTransport = oldT })
	return srv
}

func TestFetchDownloadsVerifiesAndRefusesRedirectsAway(t *testing.T) {
	body := strings.Repeat("jar", 1000)
	sum := sha256.Sum256([]byte(body))
	mux := http.NewServeMux()
	mux.HandleFunc("/server.jar", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") != "Stokehold-helper" {
			http.Error(w, "unexpected headers", 400)
			return
		}
		w.Write([]byte(body))
	})
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/server.jar", http.StatusFound)
	})
	mux.HandleFunc("/missing", http.NotFound)
	srv := startTLS(t, mux)
	b, home := fmBackend(t)

	if r := call(b, Request{Op: "fetch", Path: "server.jar", URL: srv.URL + "/server.jar", SHA256: hex.EncodeToString(sum[:])}); !r.OK {
		t.Fatalf("fetch: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "server.jar")); string(got) != body {
		t.Error("downloaded content differs")
	}
	bad := strings.Repeat("0", 64)
	if r := call(b, Request{Op: "fetch", Path: "server.jar", URL: srv.URL + "/server.jar", SHA256: bad}); r.OK || !strings.Contains(r.Err, "checksum") {
		t.Errorf("a wrong checksum must fail: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "server.jar")); string(got) != body {
		t.Error("a failed fetch must keep the previous file")
	}
	if r := call(b, Request{Op: "fetch", Path: "x.jar", URL: srv.URL + "/away"}); r.OK {
		t.Errorf("a redirect to a host outside the list must fail: %+v", r)
	}
	if r := call(b, Request{Op: "fetch", Path: "x.jar", URL: srv.URL + "/missing"}); r.OK || !strings.Contains(r.Err, "404") {
		t.Errorf("an error status: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(home, "x.jar")); err == nil {
		t.Error("nothing must be written after a failure")
	}
	old := maxFetch
	maxFetch = 100
	defer func() { maxFetch = old }()
	if r := call(b, Request{Op: "fetch", Path: "big.jar", URL: srv.URL + "/server.jar"}); r.OK {
		t.Errorf("the size cap must apply: %+v", r)
	}
}

func TestHTTPGetJSONNeedsNoUserAndCapsTheAnswer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/versions", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"versions":["1.21"]}`)) })
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("x", maxJSON+10))) })
	srv := startTLS(t, mux)
	r := HandleFull(Request{Op: "http-get-json", URL: srv.URL + "/versions"}, Backend{})
	if !r.OK || r.Out != `{"versions":["1.21"]}` {
		t.Errorf("get: %+v", r)
	}
	if r := HandleFull(Request{Op: "http-get-json", URL: srv.URL + "/huge"}, Backend{}); r.OK {
		t.Errorf("a 4 MiB cap: %+v", r)
	}
}

func TestPublicIPRule(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.5", "169.254.1.1", "::1", "0.0.0.0", "172.16.0.9"} {
		if publicIP(parseIP(s)) {
			t.Errorf("%s must not be reachable by the helper", s)
		}
	}
	if !publicIP(parseIP("93.184.216.34")) {
		t.Error("a public address must be allowed")
	}
}

func parseIP(s string) net.IP { return net.ParseIP(s) }
