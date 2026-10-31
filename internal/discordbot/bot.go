// Package discordbot answers Discord interactions over HTTP: the role buttons
// in the roles channel. There is no gateway connection, so the service stays
// tiny and only needs one public HTTPS endpoint.
package discordbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/discordsetup"
)

const maxBody = 64 << 10

const (
	typePing      = 1
	typeComponent = 3
	respPong      = 1
	respMessage   = 4
	flagEphemeral = 64
)

type Handler struct {
	PublicKey ed25519.PublicKey
	API       *discordsetup.Client
	SelfRoles map[string]string // button key -> role name; only these can be toggled
	AppID     string            // application id, needed to edit deferred answers
	Mod       *Mod              // slash commands; nil switches them off
	Tickets   *Tickets          // shop tickets; nil switches them off
	Panel     *Panel            // /servers, /server, /metrics against a Stokehold panel; nil switches them off
	Reports   *Reports          // /report and the complaint buttons; nil switches them off
	Logs      *Logs             // writes every command into the bot log; nil switches that off
	Now       func() time.Time  // tests only

	wg sync.WaitGroup // deferred answers still running
}

type interaction struct {
	Type      int    `json:"type"`
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Token     string `json:"token"`
	Message   struct {
		ID string `json:"id"`
	} `json:"message"`
	Member struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
		Roles []string `json:"roles"`
	} `json:"member"`
	Data struct {
		CustomID string   `json:"custom_id"`
		Name     string   `json:"name"`
		Options  []option `json:"options"`
		TargetID string   `json:"target_id"` // message menu commands
		Resolved struct {
			Users    map[string]resolvedUser `json:"users"`
			Messages map[string]struct {
				Content string       `json:"content"`
				Author  resolvedUser `json:"author"`
			} `json:"messages"`
		} `json:"resolved"`
	} `json:"data"`
}

type resolvedUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil || !h.verified(r, body) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}
	var in interaction
	if json.Unmarshal(body, &in) != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	switch in.Type {
	case typePing:
		reply(w, map[string]any{"type": respPong})
	case typeComponent:
		if strings.HasPrefix(in.Data.CustomID, "tkt:") {
			h.deferred(w, in, h.ticket)
			return
		}
		if strings.HasPrefix(in.Data.CustomID, "rep:") && h.Reports != nil {
			h.deferred(w, in, h.Reports.press)
			return
		}
		reply(w, h.component(r.Context(), in))
	case typeCommand:
		if h.Logs != nil {
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				h.Logs.Command(ctx, in)
			}()
		}
		if h.Reports != nil && h.Reports.handles(in.Data.Name) {
			h.deferred(w, in, h.Reports.run)
			return
		}
		if h.Panel != nil && h.Panel.handles(in.Data.Name) {
			h.deferred(w, in, h.Panel.run)
			return
		}
		if h.Mod == nil {
			http.Error(w, "commands are off", http.StatusBadRequest)
			return
		}
		h.deferred(w, in, h.Mod.run)
	default:
		http.Error(w, "unsupported interaction", http.StatusBadRequest)
	}
}

// verified checks the ed25519 signature Discord puts on every request, and
// rejects stale timestamps so a captured request cannot be replayed later.
func (h *Handler) verified(r *http.Request, body []byte) bool {
	sig, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	ts := r.Header.Get("X-Signature-Timestamp")
	if err != nil || len(sig) != ed25519.SignatureSize || ts == "" {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	if d := now().Unix() - sec; d > 300 || d < -300 {
		return false
	}
	return ed25519.Verify(h.PublicKey, append([]byte(ts), body...), sig)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func message(text string) map[string]any {
	return map[string]any{"type": respMessage, "data": map[string]any{"content": text, "flags": flagEphemeral}}
}

// component toggles a self-assignable role for the member who clicked.
func (h *Handler) component(ctx context.Context, in interaction) map[string]any {
	key, ok := strings.CutPrefix(in.Data.CustomID, "role:")
	roleName, listed := h.SelfRoles[key]
	if !ok || !listed || in.GuildID == "" || in.Member.User.ID == "" {
		return message("Эта кнопка не поддерживается.")
	}
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond) // Discord gives us 3 seconds
	defer cancel()

	var roles []struct{ ID, Name string }
	if err := h.API.Do(ctx, "GET", "/guilds/"+in.GuildID+"/roles", nil, &roles); err != nil {
		return message("Не удалось получить роли, попробуйте позже.")
	}
	roleID := ""
	for _, r := range roles {
		if r.Name == roleName {
			roleID = r.ID
		}
	}
	if roleID == "" {
		return message("Такой роли на сервере нет.")
	}
	has := false
	for _, id := range in.Member.Roles {
		has = has || id == roleID
	}
	path := "/guilds/" + in.GuildID + "/members/" + in.Member.User.ID + "/roles/" + roleID
	method, done := "PUT", "Роль выдана: "
	if has {
		method, done = "DELETE", "Роль снята: "
	}
	if err := h.API.Do(ctx, method, path, nil, nil); err != nil {
		return message("Не удалось изменить роль, попробуйте позже.")
	}
	return message(done + roleName)
}

// deferred answers at once and finishes the work in the background, so a
// command that needs several Discord calls is not held to the 3 second limit.
func (h *Handler) deferred(w http.ResponseWriter, in interaction, run func(context.Context, interaction) string) {
	reply(w, map[string]any{"type": respDefer, "data": map[string]any{"flags": flagEphemeral}})
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		text := run(ctx, in)
		_ = h.API.Do(ctx, "PATCH", "/webhooks/"+h.AppID+"/"+in.Token+"/messages/@original",
			map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}}, nil)
	}()
}

func (h *Handler) ticket(ctx context.Context, in interaction) string {
	if h.Tickets == nil {
		return "Тикеты выключены."
	}
	parts := strings.SplitN(in.Data.CustomID, ":", 3)
	switch {
	case len(parts) == 2 && parts[1] == "open":
		return h.Tickets.open(ctx, h, in)
	case len(parts) == 3 && parts[1] == "claim":
		return h.Tickets.claim(ctx, h, in, parts[2])
	case len(parts) == 3 && parts[1] == "close":
		return h.Tickets.close(ctx, h, in, parts[2])
	}
	return "Эта кнопка не поддерживается."
}
