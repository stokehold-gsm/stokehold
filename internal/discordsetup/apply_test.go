package discordsetup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fake is just enough of the Discord REST API to check Apply end to end.
type fake struct {
	mu       sync.Mutex
	roles    []map[string]any
	chans    []map[string]any
	pins     map[string][]string
	bodies   []map[string]any
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
		f.bodies = append(f.bodies, body)
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
	roles, chans, posts := len(f.roles), len(f.chans), len(f.bodies)
	if roles != len(Default().Roles) || chans == 0 || posts == 0 {
		t.Fatalf("first run created too little: roles=%d chans=%d posts=%d", roles, chans, posts)
	}
	run(t, f, Options{})
	if len(f.roles) != roles || len(f.chans) != chans || len(f.bodies) != posts {
		t.Errorf("second run changed things: roles %d->%d chans %d->%d posts %d->%d",
			roles, len(f.roles), chans, len(f.chans), posts, len(f.bodies))
	}
	if f.lastAuth != "Bot tok" {
		t.Errorf("auth header: %q", f.lastAuth)
	}
}

func TestVoiceChannelsHaveNoTopic(t *testing.T) {
	f := &fake{}
	run(t, f, Options{})
	voice := 0
	for _, c := range f.chans {
		if c["type"] == float64(2) {
			voice++
			if _, has := c["topic"]; has {
				t.Errorf("voice channel %v must not carry a topic", c["name"])
			}
		}
	}
	if voice == 0 {
		t.Error("no voice channels created")
	}
}

func TestPlaceholdersAreResolved(t *testing.T) {
	f := &fake{}
	run(t, f, Options{})
	raw, _ := json.Marshal(f.bodies)
	if strings.Contains(string(raw), "{#") {
		t.Errorf("an unresolved {#key} placeholder was posted")
	}
	// json.Marshal escapes < and > as < and >
	if !regexp.MustCompile(`\\u003c#10\d+\\u003e`).Match(raw) {
		t.Error("expected channel mentions in posted messages")
	}
}

func TestRemoveLegacyOnlyTouchesListedNames(t *testing.T) {
	f := &fake{chans: []map[string]any{
		{"id": "a", "name": "ru-general", "type": float64(0)},
		{"id": "b", "name": "RU", "type": float64(4)},
		{"id": "c", "name": "my-own-channel", "type": float64(0)},
	}}
	run(t, f, Options{RemoveLegacy: true})
	if strings.Join(f.deleted, ",") != "a,b" {
		t.Errorf("deleted %v, want a,b and never my-own-channel", f.deleted)
	}
	run(t, &fake{chans: []map[string]any{{"id": "x", "name": "ru-general", "type": float64(0)}}}, Options{})
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
	if ro[1].ID != "g" || ro[1].Deny != perm(PermSendMessages|PermAddReactions) {
		t.Errorf("read-only must deny SEND and reactions to everyone: %+v", ro)
	}
	hidden := Overwrites(Channel{OnlyFor: []string{RolePro}}, "g", "bot", ids)
	if hidden[1].ID != "g" || hidden[1].Deny != perm(PermViewChannel) || hidden[2].ID != "pro" {
		t.Errorf("restricted channel must hide from everyone and show to pro: %+v", hidden)
	}
	if ro[0].ID != "bot" || ro[0].Type != 1 {
		t.Errorf("bot must always have its own overwrite: %+v", ro[0])
	}
}

// Discord rejects oversized messages with a 400 at the very end of a run,
// so the limits are checked here instead.
func TestPlanFitsDiscordLimits(t *testing.T) {
	p := Default()
	keys := map[string]bool{}
	for _, cat := range p.Categories {
		for _, ch := range cat.Channels {
			if ch.Key == "" || keys[ch.Key] {
				t.Errorf("empty or duplicate channel key %q", ch.Key)
			}
			keys[ch.Key] = true
			if utf8.RuneCountInString(ch.Name) > 100 || len(ch.Topic) > 1024 {
				t.Errorf("channel %q: name or topic too long", ch.Name)
			}
		}
	}
	placeholder := regexp.MustCompile(`\{#([a-z0-9-]+)\}`)
	check := func(where, s string) {
		for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
			if !keys[m[1]] {
				t.Errorf("%s: unknown channel key {#%s}", where, m[1])
			}
		}
	}
	for _, cat := range p.Categories {
		for _, ch := range cat.Channels {
			for _, m := range ch.Messages {
				if len(m.Embeds) > 10 || len(m.Rows) > 5 {
					t.Errorf("%s: too many embeds or button rows", ch.Key)
				}
				for _, e := range m.Embeds {
					total := utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description) + utf8.RuneCountInString(e.Footer)
					if utf8.RuneCountInString(e.Title) > 256 || utf8.RuneCountInString(e.Description) > 4096 || len(e.Fields) > 25 {
						t.Errorf("%s: embed over limits", ch.Key)
					}
					check(ch.Key, e.Title+e.Description)
					for _, f := range e.Fields {
						total += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
						if utf8.RuneCountInString(f.Name) > 256 || utf8.RuneCountInString(f.Value) > 1024 {
							t.Errorf("%s: field %q over limits", ch.Key, f.Name)
						}
						check(ch.Key, f.Value)
					}
					if total > 6000 {
						t.Errorf("%s: embed totals %d chars, limit is 6000", ch.Key, total)
					}
				}
				for _, row := range m.Rows {
					if len(row) == 0 || len(row) > 5 {
						t.Errorf("%s: button row must hold 1-5 buttons", ch.Key)
					}
				}
			}
		}
	}
}

func TestSelfRolesMatchButtonsAndRoles(t *testing.T) {
	p := Default()
	roleNames := map[string]bool{}
	for _, r := range p.Roles {
		roleNames[r.Name] = true
	}
	self := map[string]string{}
	for _, s := range p.SelfRoles {
		if !roleNames[s.RoleName] {
			t.Errorf("self role %q points to a role the plan does not create", s.RoleName)
		}
		self[s.Key] = s.RoleName
	}
	for _, cat := range p.Categories {
		for _, ch := range cat.Channels {
			for _, m := range ch.Messages {
				for _, row := range m.Rows {
					for _, b := range row {
						if _, ok := self[b.Key]; !ok {
							t.Errorf("button %q has no self role", b.Key)
						}
					}
				}
			}
		}
	}
	for _, tier := range []string{RolePro, RoleHosting, RoleBeta, RoleMod, RoleFounder, RoleFree} {
		for _, name := range self {
			if name == tier {
				t.Errorf("tier or staff role %q must never be self-assignable", tier)
			}
		}
	}
}
