package discordbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

// guild is a tiny Discord: fixed roles, channels and members, and a record of
// every write.
type guild struct {
	mu      sync.Mutex
	writes  []string
	topics  map[string]string
	members map[string]string // user id -> JSON of the member object
	posts   []string
}

func (g *guild) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := r.URL.Path
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	rec := func() {
		g.writes = append(g.writes, r.Method+" "+p)
		if r.Method == "POST" {
			b, _ := json.Marshal(body)
			g.posts = append(g.posts, string(b))
		}
	}
	switch {
	case r.Method == "GET" && p == "/guilds/g/roles":
		w.Write([]byte(`[{"id":"rF","name":"Founder"},{"id":"rC","name":"Curator"},{"id":"rM","name":"Moderator"},{"id":"rH","name":"Helper"},{"id":"rSC","name":"Shop Curator"},{"id":"rO","name":"Shop Operator"}]`))
	case r.Method == "GET" && p == "/guilds/g/channels":
		out := `[{"id":"cLog","name":"` + discordsetup.LogChannel + `"},{"id":"cCat","name":"` + discordsetup.TicketCategory + `","type":4},` +
			`{"id":"cNew","name":"` + discordsetup.TicketNotify + `"},{"id":"cOrd","name":"` + discordsetup.TicketOrders + `"}`
		for id, topic := range g.topics {
			out += `,{"id":"` + id + `","name":"ticket-x","topic":"` + topic + `","parent_id":"cCat"}`
		}
		w.Write([]byte(out + `]`))
	case r.Method == "GET" && strings.HasPrefix(p, "/guilds/g/members/"):
		m, ok := g.members[strings.TrimPrefix(p, "/guilds/g/members/")]
		if !ok {
			http.Error(w, "unknown", 404)
			return
		}
		w.Write([]byte(m))
	case r.Method == "GET" && p == "/users/@me":
		w.Write([]byte(`{"id":"bot"}`))
	case r.Method == "GET":
		w.Write([]byte(`[]`))
	case r.Method == "POST" && p == "/guilds/g/channels":
		rec()
		w.Write([]byte(`{"id":"cT1"}`))
	default:
		rec()
		w.WriteHeader(204)
	}
}

func modRig(t *testing.T) (*Handler, *guild) {
	g := &guild{topics: map[string]string{}, members: map[string]string{
		"victim": `{"user":{},"roles":[]}`,
		"mod2":   `{"user":{},"roles":["rM"]}`,
		"robot":  `{"user":{"bot":true},"roles":[]}`,
		"shopie": `{"user":{},"roles":["rO"]}`,
	}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	api := &discordsetup.Client{Base: srv.URL, Token: "t", HTTP: srv.Client()}
	warns, _ := OpenWarns("")
	h := &Handler{API: api, AppID: "app",
		Mod:     &Mod{API: api, LogName: discordsetup.LogChannel, Warns: warns, Now: func() time.Time { return now }},
		Tickets: &Tickets{Category: discordsetup.TicketCategory, Notify: discordsetup.TicketNotify, Orders: discordsetup.TicketOrders}}
	return h, g
}

func cmd(name string, roles []string, opts map[string]any) interaction {
	var in interaction
	in.GuildID, in.ChannelID = "g", "cx"
	in.Member.User.ID, in.Member.Roles = "invoker", roles
	in.Data.Name = name
	for k, v := range opts {
		b, _ := json.Marshal(v)
		in.Data.Options = append(in.Data.Options, option{Name: k, Value: b})
	}
	return in
}

func (g *guild) wrote(s string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, w := range g.writes {
		if w == s {
			return true
		}
	}
	return false
}

func TestCommandsRespectRanks(t *testing.T) {
	h, g := modRig(t)
	ctx := context.Background()
	user := map[string]any{"user": "victim", "reason": "п.2 спам", "minutes": 30}

	if out := h.Mod.run(ctx, cmd("kick", nil, user)); !strings.Contains(out, "нет прав") || g.wrote("DELETE /guilds/g/members/victim") {
		t.Errorf("plain member: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("timeout", []string{"rH"}, user)); !strings.Contains(out, "не больше 10") {
		t.Errorf("helper 30 min: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("timeout", []string{"rH"}, map[string]any{"user": "victim", "reason": "флуд", "minutes": 10})); !strings.Contains(out, "выдан") || !g.wrote("PATCH /guilds/g/members/victim") {
		t.Errorf("helper 10 min: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("warn", []string{"rH"}, user)); !strings.Contains(out, "нет прав") {
		t.Errorf("helper warn: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("kick", []string{"rM"}, user)); !strings.Contains(out, "исключён") {
		t.Errorf("moderator kick: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("ban", []string{"rM"}, user)); !strings.Contains(out, "нет прав") || g.wrote("PUT /guilds/g/bans/victim") {
		t.Errorf("moderator ban: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("timeout", []string{"rM"}, map[string]any{"user": "victim", "reason": "r", "minutes": 8 * 24 * 60})); !strings.Contains(out, "до 7 дней") {
		t.Errorf("moderator 8 days: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("ban", []string{"rC"}, user)); !strings.Contains(out, "забанен") || !g.wrote("PUT /guilds/g/bans/victim") {
		t.Errorf("curator ban: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("unban", []string{"rC"}, map[string]any{"user_id": "123456789", "reason": "ошибка"})); !strings.Contains(out, "Бан снят") {
		t.Errorf("curator unban: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("unban", []string{"rC"}, map[string]any{"user_id": "x; drop", "reason": "r"})); !strings.Contains(out, "id") {
		t.Errorf("bad id: %q", out)
	}
}

func TestStaffAndBotsAreProtected(t *testing.T) {
	h, g := modRig(t)
	ctx := context.Background()
	for _, target := range []string{"mod2", "robot", "shopie"} {
		out := h.Mod.run(ctx, cmd("kick", []string{"rM"}, map[string]any{"user": target, "reason": "r"}))
		if strings.Contains(out, "исключён") || g.wrote("DELETE /guilds/g/members/"+target) {
			t.Errorf("a moderator acted on %s: %q", target, out)
		}
	}
	if out := h.Mod.run(ctx, cmd("kick", []string{"rC"}, map[string]any{"user": "mod2", "reason": "r"})); !strings.Contains(out, "исключён") {
		t.Errorf("curator on moderator: %q", out)
	}
	if out := h.Mod.run(ctx, cmd("kick", []string{"rM"}, map[string]any{"user": "invoker", "reason": "r"})); !strings.Contains(out, "другого") {
		t.Errorf("self action: %q", out)
	}
}

func TestWarningsAreCountedAndLogged(t *testing.T) {
	h, g := modRig(t)
	ctx := context.Background()
	in := cmd("warn", []string{"rM"}, map[string]any{"user": "victim", "reason": "п.4 реклама"})
	var out string
	for i := 0; i < 3; i++ {
		out = h.Mod.run(ctx, in)
	}
	if !strings.Contains(out, "таймаут на сутки") {
		t.Errorf("the third warning should point at the policy: %q", out)
	}
	if list := h.Mod.run(ctx, cmd("warnings", []string{"rH"}, map[string]any{"user": "victim"})); !strings.Contains(list, "(3)") {
		t.Errorf("list: %q", list)
	}
	if !g.wrote("POST /channels/cLog/messages") {
		t.Error("warnings must be written to the moderation log")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.posts {
		if !strings.Contains(p, `"parse":[]`) {
			t.Errorf("a log post must not ping anyone: %s", p)
		}
	}
}

func TestDeferredAnswerIsEditedIn(t *testing.T) {
	h, g := modRig(t)
	rec := httptest.NewRecorder()
	h.deferred(rec, cmd("warnings", []string{"rH"}, map[string]any{"user": "victim"}), h.Mod.run)
	h.wg.Wait()
	if !strings.Contains(rec.Body.String(), `"type":5`) {
		t.Errorf("not deferred: %s", rec.Body)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.writes) == 0 || !strings.HasPrefix(g.writes[len(g.writes)-1], "PATCH /webhooks/app/") {
		t.Errorf("the result was not edited into the answer: %v", g.writes)
	}
}

func TestTicketFlow(t *testing.T) {
	h, g := modRig(t)
	ctx := context.Background()
	open := interaction{GuildID: "g", ChannelID: "cx"}
	open.Member.User.ID, open.Member.User.Username = "cust", "Иван"
	open.Data.CustomID = "tkt:open"
	if out := h.ticket(ctx, open); !strings.Contains(out, "<#cT1>") {
		t.Fatalf("open: %q", out)
	}
	g.mu.Lock()
	var created, notified string
	for _, p := range g.posts {
		if strings.Contains(p, "permission_overwrites") {
			created = p
		}
		if strings.Contains(p, "Новый тикет") {
			notified = p
		}
	}
	g.mu.Unlock()
	for _, id := range []string{"rO", "rM", "rC", "rH"} {
		if strings.Contains(created, `"id":"`+id+`"`) {
			t.Errorf("role %s must not see a fresh ticket: %s", id, created)
		}
	}
	for _, id := range []string{"cust", "rSC", "rF", "bot"} {
		if !strings.Contains(created, `"id":"`+id+`"`) {
			t.Errorf("%s must see the ticket: %s", id, created)
		}
	}
	if !strings.Contains(notified, "rO") {
		t.Errorf("operators were not notified: %s", notified)
	}

	g.mu.Lock()
	g.topics["cT1"] = "ticket:cust:-"
	g.mu.Unlock()
	if out := h.ticket(ctx, open); !strings.Contains(out, "уже есть") {
		t.Errorf("second ticket: %q", out)
	}

	claim := interaction{GuildID: "g", ChannelID: "cNew"}
	claim.Message.ID = "m1"
	claim.Data.CustomID = "tkt:claim:cT1"
	claim.Member.User.ID, claim.Member.Roles = "op1", []string{"rM"} // a moderator is not shop staff
	if out := h.ticket(ctx, claim); !strings.Contains(out, "сотрудники магазина") || g.wrote("PUT /channels/cT1/permissions/op1") {
		t.Errorf("moderator claim: %q", out)
	}
	claim.Member.Roles = []string{"rO"}
	if out := h.ticket(ctx, claim); !strings.Contains(out, "Вы взяли") || !g.wrote("PUT /channels/cT1/permissions/op1") {
		t.Errorf("operator claim: %q", out)
	}
	g.mu.Lock()
	g.topics["cT1"] = "ticket:cust:op1"
	g.mu.Unlock()
	claim.Member.User.ID = "op2"
	if out := h.ticket(ctx, claim); !strings.Contains(out, "уже взял") || g.wrote("PUT /channels/cT1/permissions/op2") {
		t.Errorf("second claim: %q", out)
	}

	closeIt := interaction{GuildID: "g", ChannelID: "cT1"}
	closeIt.Data.CustomID = "tkt:close:cT1"
	closeIt.Member.User.ID = "stranger"
	if out := h.ticket(ctx, closeIt); !strings.Contains(out, "Закрыть тикет может") || g.wrote("DELETE /channels/cT1") {
		t.Errorf("stranger close: %q", out)
	}
	closeIt.Member.User.ID = "cust"
	if out := h.ticket(ctx, closeIt); !strings.Contains(out, "закрыт") || !g.wrote("DELETE /channels/cT1") {
		t.Errorf("customer close: %q", out)
	}
	closeIt.Data.CustomID = "tkt:close:cLog"
	h.ticket(ctx, closeIt)
	if g.wrote("DELETE /channels/cLog") {
		t.Error("a non-ticket channel was deleted")
	}
}

func TestAuditLogBecomesEventLines(t *testing.T) {
	var page string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(page)) }))
	defer srv.Close()
	e := &Events{API: &discordsetup.Client{Base: srv.URL, Token: "t", HTTP: srv.Client()}, GuildID: "g"}
	page = `{"audit_log_entries":[{"id":"20","user_id":"u1","target_id":"u2","action_type":22,"reason":"спам"},{"id":"10","user_id":"bot","action_type":20}]}`
	if lines, err := e.Poll(context.Background(), "bot"); err != nil || len(lines) != 0 {
		t.Fatalf("the first poll only records the position: %v %v", lines, err)
	}
	page = `{"audit_log_entries":[{"id":"31","user_id":"bot","action_type":24},{"id":"30","user_id":"u1","target_id":"c9","action_type":12},{"id":"20","user_id":"u1","action_type":22}]}`
	lines, err := e.Poll(context.Background(), "bot")
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "Удалён канал") {
		t.Errorf("only the new foreign entry should be reported once: %v %v", lines, err)
	}
	if again, _ := e.Poll(context.Background(), "bot"); len(again) != 0 {
		t.Errorf("replayed: %v", again)
	}
}
