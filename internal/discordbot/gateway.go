package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Gateway intents the logs need. The last two are privileged: they must be
// switched on in the Developer Portal, and without them Discord closes the
// connection with 4014, after which the gateway retries without them.
const (
	intentGuilds         = 1 << 0
	intentMembers        = 1 << 1 // privileged
	intentModeration     = 1 << 2
	intentInvites        = 1 << 6
	intentVoice          = 1 << 7
	intentMessages       = 1 << 9
	intentMessageContent = 1 << 15 // privileged
	intentAutomodExec    = 1 << 21

	baseIntents = intentGuilds | intentModeration | intentInvites | intentVoice | intentMessages | intentAutomodExec
	allIntents  = baseIntents | intentMembers | intentMessageContent
)

const defaultGatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

type frame struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// Gateway is a minimal Discord gateway client: it keeps one connection alive,
// resumes after a drop, and hands every dispatch to OnEvent. Sharding is not
// needed for one community server.
type Gateway struct {
	Token   string
	GuildID string
	URL     string       // tests only
	HTTP    *http.Client // nil: a client honouring HTTPS_PROXY (the bot reaches Discord through a proxy)
	OnEvent func(ctx context.Context, name string, data json.RawMessage)
	Logf    func(string, ...any)

	intents   int
	sessionID string
	resumeURL string
	seq       atomic.Int64
	// Granted is true while the connection runs with the privileged intents.
	Granted atomic.Bool
}

func (g *Gateway) logf(f string, a ...any) {
	if g.Logf != nil {
		g.Logf(f, a...)
	}
}

// Run connects until ctx ends. Privileged intents that Discord refuses are
// dropped one by one, message content first, and the loss is logged once.
func (g *Gateway) Run(ctx context.Context) {
	g.intents = allIntents
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		err := g.session(ctx)
		switch websocket.CloseStatus(err) {
		case 4004:
			g.logf("gateway: the bot token was refused, giving up")
			return
		case 4014:
			g.sessionID = ""
			if g.dropIntent() {
				continue
			}
		case 4007, 4009:
			g.sessionID = "" // the session is gone, identify again
		}
		if err != nil && ctx.Err() == nil {
			g.logf("gateway: %v (retry in %s)", err, backoff)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// dropIntent gives up one privileged intent; false when none is left to drop.
func (g *Gateway) dropIntent() bool {
	switch {
	case g.intents&intentMessageContent != 0:
		g.intents &^= intentMessageContent
		g.logf("gateway: Message Content intent is off in the Developer Portal: edited and deleted messages will show no text")
	case g.intents&intentMembers != 0:
		g.intents &^= intentMembers
		g.logf("gateway: Server Members intent is off in the Developer Portal: joins, leaves and role changes will not be logged")
	default:
		g.logf("gateway: Discord refuses even the basic intents")
		return false
	}
	return true
}

func (g *Gateway) session(ctx context.Context) error {
	url := g.URL
	if url == "" {
		url = defaultGatewayURL
	}
	if g.sessionID != "" && g.resumeURL != "" {
		url = g.resumeURL
	}
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 20) // GUILD_CREATE carries the whole member list
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var hello struct {
		Interval float64 `json:"heartbeat_interval"`
	}
	f, err := g.read(ctx, conn)
	if err != nil {
		return err
	}
	if f.Op != 10 || json.Unmarshal(f.D, &hello) != nil {
		return errors.New("no hello from the gateway")
	}

	var acked atomic.Bool
	acked.Store(true)
	beat := func() error {
		var d any
		if s := g.seq.Load(); s > 0 {
			d = s
		}
		return send(ctx, conn, 1, d)
	}
	hbErr := make(chan error, 1)
	go func() {
		every := time.Duration(hello.Interval) * time.Millisecond
		if every <= 0 {
			every = 40 * time.Second
		}
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if !acked.Swap(false) {
					hbErr <- errors.New("heartbeat not acknowledged")
					return
				}
				if err := beat(); err != nil {
					hbErr <- err
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	if g.sessionID != "" {
		err = send(ctx, conn, 6, map[string]any{"token": g.Token, "session_id": g.sessionID, "seq": g.seq.Load()})
	} else {
		g.seq.Store(0)
		err = send(ctx, conn, 2, map[string]any{
			"token": g.Token, "intents": g.intents,
			"properties": map[string]string{"os": "linux", "browser": "stokehold", "device": "stokehold"},
		})
	}
	if err != nil {
		return err
	}

	frames := make(chan frame)
	readErr := make(chan error, 1)
	go func() {
		for {
			f, err := g.read(ctx, conn)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case err := <-readErr:
			return err
		case err := <-hbErr:
			return err
		case f := <-frames:
			switch f.Op {
			case 0:
				if f.S != nil {
					g.seq.Store(*f.S)
				}
				g.dispatch(ctx, conn, f)
			case 1:
				beat()
			case 7:
				return errors.New("gateway asked to reconnect")
			case 9:
				var resumable bool
				json.Unmarshal(f.D, &resumable)
				if !resumable {
					g.sessionID = ""
				}
				time.Sleep(2 * time.Second)
				return errors.New("invalid session")
			case 11:
				acked.Store(true)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *Gateway) dispatch(ctx context.Context, conn *websocket.Conn, f frame) {
	switch f.T {
	case "READY":
		var r struct {
			SessionID string `json:"session_id"`
			ResumeURL string `json:"resume_gateway_url"`
		}
		json.Unmarshal(f.D, &r)
		g.sessionID = r.SessionID
		if r.ResumeURL != "" {
			g.resumeURL = r.ResumeURL + "/?v=10&encoding=json"
		}
		g.Granted.Store(g.intents&intentMessageContent != 0)
		return
	case "GUILD_CREATE":
		// the cache of old names, roles and voice states starts from this event;
		// the member list is asked for in full because GUILD_CREATE may be partial
		if g.intents&intentMembers != 0 && g.GuildID != "" {
			send(ctx, conn, 8, map[string]any{"guild_id": g.GuildID, "query": "", "limit": 0})
		}
	}
	if g.OnEvent != nil {
		g.OnEvent(ctx, f.T, f.D)
	}
}

func (g *Gateway) read(ctx context.Context, conn *websocket.Conn) (frame, error) {
	var f frame
	_, b, err := conn.Read(ctx)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(b, &f)
}

func send(ctx context.Context, conn *websocket.Conn, op int, d any) error {
	b, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}
