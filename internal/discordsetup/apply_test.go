package discordsetup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fake is just enough of the Discord REST API to check Apply end to end.
type fake struct {
	mu       sync.Mutex
	roles    []map[string]any
	chans    []map[string]any
	pins     map[string][]string
	posts    int
	deleted  []string
	nextID   int
	lastAuth string
}

func (f *fake) id() string { f.nextID++; return strconv.Itoa(1000 + f.nextID) }

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAuth = r.Header.Get("Authorization")
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	out := func(v any) { json.NewEncoder(w).Encode(v) }
	p := r.URL.Path
	switch {
	case p == "/users/@me":
		out(map[string]string{"id": "bot1"})
	case p == "/guilds/g/roles" && r.Method == "GET":
		out(f.roles)
	case p == "/guilds/g/roles":
		body["id"] = f.id()
		f.roles = append(f.roles, body)
		out(body)
	case p == "/guilds/g/channels" && r.Method == "GET":
		out(f.chans)
	case p == "/guilds/g/channels":
		body["id"] = f.id()
		f.chans = append(f.chans, body)
		out(body)
	case r.Method == "DELETE" && strings.HasPrefix(p, "/channels/"):
		id := strings.TrimPrefix(p, "/channels/")
		f.deleted = append(f.deleted, id)
		for i, c := range f.chans {
			if c["id"] == id {
				f.chans = append(f.chans[:i], f.chans[i+1:]...)
				break
			}
		}
	case strings.HasSuffix(p, "/messages/pins") && r.Method == "GET":
		ch := strings.Split(p, "/")[2]
		items := []any{}
		for range f.pins[ch] {
			items = append(items, map[string]any{})
		}
		out(map[string]any{"items": items})
	case strings.HasSuffix(p, "/messages") && r.Method == "POST":
		f.posts++
		out(map[string]string{"id": f.id()})
	case strings.Contains(p, "/messages/pins/") && r.Method == "PUT":
		ch := strings.Split(p, "/")[2]
		if f.pins == nil {
			f.pins = map[string][]string{}
		}
		f.pins[ch] = append(f.pins[ch], strings.Split(p, "/")[5])
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+r.Method+" "+p, http.StatusTeapot)
	}
}

func run(t *testing.T, f *fake, opt Options) {
	t.Helper()
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: "tok", HTTP: srv.Client()}
	if err := Apply(context.Background(), c, "g", Default(), opt); err != nil {
		t.Fatal(err)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	f := &fake{}
	run(t, f, Options{})
	roles, chans, posts := len(f.roles), len(f.chans), f.posts
	if roles != len(Default().Roles) || chans == 0 || posts == 0 {
		t.Fatalf("first run created too little: roles=%d chans=%d posts=%d", roles, chans, posts)
	}
	run(t, f, Options{})
	if len(f.roles) != roles || len(f.chans) != chans || f.posts != posts {
		t.Errorf("second run changed things: roles %d->%d chans %d->%d posts %d->%d",
			roles, len(f.roles), chans, len(f.chans), posts, f.posts)
	}
	if f.lastAuth != "Bot tok" {
		t.Errorf("auth header: %q", f.lastAuth)
	}
}

func TestCleanDefaultsOnlyTouchesDefaults(t *testing.T) {
	f := &fake{chans: []map[string]any{
		{"id": "a", "name": "основной", "type": float64(0)},
		{"id": "b", "name": "Текстовые каналы", "type": float64(4)},
		{"id": "c", "name": "my-own-channel", "type": float64(0)},
	}}
	run(t, f, Options{CleanDefaults: true})
	if strings.Join(f.deleted, ",") != "a,b" {
		t.Errorf("deleted %v, want a,b (and never my-own-channel)", f.deleted)
	}
}

func TestOverwrites(t *testing.T) {
	ids := map[string]string{RoleMod: "mod", RolePro: "pro"}
	ro := Overwrites(Channel{ReadOnly: true}, "g", "bot", ids)
	if ro[1].ID != "g" || ro[1].Deny != perm(PermSendMessages) {
		t.Errorf("read-only must deny SEND to everyone: %+v", ro)
	}
	hidden := Overwrites(Channel{OnlyFor: []string{RolePro}}, "g", "bot", ids)
	if hidden[1].ID != "g" || hidden[1].Deny != perm(PermViewChannel) || hidden[2].ID != "pro" {
		t.Errorf("restricted channel must hide from everyone and show to pro: %+v", hidden)
	}
	if ro[0].ID != "bot" || ro[0].Type != 1 {
		t.Errorf("bot must always have its own overwrite: %+v", ro[0])
	}
}

func TestPlanShape(t *testing.T) {
	seen := map[string]bool{}
	for _, cat := range Default().Categories {
		for _, ch := range cat.Channels {
			if seen[ch.Name] || ch.Name != strings.ToLower(ch.Name) || strings.ContainsAny(ch.Name, " _") {
				t.Errorf("bad or duplicate channel name %q", ch.Name)
			}
			seen[ch.Name] = true
			for _, pin := range ch.Pins {
				if len(pin) > 2000 {
					t.Errorf("pin in %s exceeds Discord's 2000 char limit", ch.Name)
				}
			}
		}
	}
}
