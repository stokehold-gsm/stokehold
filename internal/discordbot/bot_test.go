package discordbot

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"stokehold/internal/discordsetup"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type rig struct {
	h     *Handler
	priv  ed25519.PrivateKey
	calls []string
	mu    sync.Mutex
}

func newRig(t *testing.T, guildRoles string) *rig {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	rg := &rig{priv: priv}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rg.mu.Lock()
		rg.calls = append(rg.calls, r.Method+" "+r.URL.Path)
		rg.mu.Unlock()
		if r.Method == "GET" {
			w.Write([]byte(guildRoles))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(api.Close)
	rg.h = &Handler{
		PublicKey: pub,
		API:       &discordsetup.Client{Base: api.URL, Token: "tok", HTTP: api.Client()},
		SelfRoles: map[string]string{"factorio": "Factorio"},
		Now:       func() time.Time { return now },
	}
	return rg
}

func (rg *rig) post(body string, ts time.Time, sign bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/discord/interactions", strings.NewReader(body))
	stamp := strconv.FormatInt(ts.Unix(), 10)
	req.Header.Set("X-Signature-Timestamp", stamp)
	sig := ed25519.Sign(rg.priv, []byte(stamp+body))
	if !sign {
		sig[0] ^= 0xff
	}
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	rec := httptest.NewRecorder()
	rg.h.ServeHTTP(rec, req)
	return rec
}

const roles = `[{"id":"r1","name":"Factorio"},{"id":"r2","name":"Pro"}]`

func click(key, memberRoles string) string {
	return `{"type":3,"guild_id":"g","member":{"user":{"id":"u1"},"roles":` + memberRoles + `},"data":{"custom_id":"` + key + `"}}`
}

func TestPingIsAnswered(t *testing.T) {
	rg := newRig(t, roles)
	rec := rg.post(`{"type":1}`, now, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"type":1`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestBadOrStaleSignatureRejected(t *testing.T) {
	rg := newRig(t, roles)
	if rec := rg.post(`{"type":1}`, now, false); rec.Code != 401 {
		t.Errorf("bad signature: %d", rec.Code)
	}
	if rec := rg.post(`{"type":1}`, now.Add(-10*time.Minute), true); rec.Code != 401 {
		t.Errorf("replayed request: %d", rec.Code)
	}
	if rec := rg.post(click("role:factorio", "[]"), now, false); len(rg.calls) != 0 || rec.Code != 401 {
		t.Errorf("unsigned click must not reach the API: %d %v", rec.Code, rg.calls)
	}
}

func TestButtonGrantsAndRemovesRole(t *testing.T) {
	rg := newRig(t, roles)
	rec := rg.post(click("role:factorio", "[]"), now, true)
	if !strings.Contains(rec.Body.String(), "Роль выдана") || rg.calls[len(rg.calls)-1] != "PUT /guilds/g/members/u1/roles/r1" {
		t.Errorf("grant: %s %v", rec.Body, rg.calls)
	}
	rec = rg.post(click("role:factorio", `["r1"]`), now, true)
	if !strings.Contains(rec.Body.String(), "Роль снята") || rg.calls[len(rg.calls)-1] != "DELETE /guilds/g/members/u1/roles/r1" {
		t.Errorf("remove: %s %v", rec.Body, rg.calls)
	}
}

func TestOnlyListedRolesCanBeToggled(t *testing.T) {
	rg := newRig(t, roles)
	// "pro" exists as a role but is not in SelfRoles, so a forged click gets nothing
	for _, id := range []string{"role:pro", "role:", "pro", "role:../x", "role:Factorio"} {
		rec := rg.post(click(id, "[]"), now, true)
		if !strings.Contains(rec.Body.String(), "не поддерживается") {
			t.Errorf("%q should be refused: %s", id, rec.Body)
		}
	}
	for _, c := range rg.calls {
		if !strings.HasPrefix(c, "GET") {
			t.Errorf("a refused click changed something: %s", c)
		}
	}
}

func TestMissingRoleOnServer(t *testing.T) {
	rg := newRig(t, `[{"id":"r2","name":"Pro"}]`)
	if rec := rg.post(click("role:factorio", "[]"), now, true); !strings.Contains(rec.Body.String(), "нет") {
		t.Errorf("%s", rec.Body)
	}
}
