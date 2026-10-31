package discordbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeGateway refuses the privileged intents (close code 4014) until the
// client asks for the basic set only, then serves READY and a few events.
type fakeGateway struct {
	mu      sync.Mutex
	intents []int
}

func (f *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := r.Context()
	write := func(v any) {
		b, _ := json.Marshal(v)
		c.Write(ctx, websocket.MessageText, b)
	}
	write(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 45000}})
	_, b, err := c.Read(ctx)
	if err != nil {
		return
	}
	var id struct {
		Op int `json:"op"`
		D  struct {
			Token   string `json:"token"`
			Intents int    `json:"intents"`
		} `json:"d"`
	}
	json.Unmarshal(b, &id)
	f.mu.Lock()
	f.intents = append(f.intents, id.D.Intents)
	f.mu.Unlock()
	if id.Op != 2 || id.D.Token != "tok" {
		c.Close(websocket.StatusCode(4004), "bad identify")
		return
	}
	if id.D.Intents&(intentMessageContent|intentMembers) != 0 {
		c.Close(websocket.StatusCode(4014), "disallowed intents")
		return
	}
	write(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{"session_id": "s1"}})
	write(map[string]any{"op": 0, "s": 2, "t": "MESSAGE_CREATE", "d": map[string]any{"id": "m1"}})
	c.Read(ctx) // stay connected until the client leaves
}

func TestGatewayDropsRefusedPrivilegedIntentsAndDeliversEvents(t *testing.T) {
	fg := &fakeGateway{}
	srv := httptest.NewServer(fg)
	defer srv.Close()

	got := make(chan string, 4)
	var logged []string
	var lmu sync.Mutex
	g := &Gateway{Token: "tok", GuildID: "g", URL: srv.URL,
		OnEvent: func(_ context.Context, name string, _ json.RawMessage) { got <- name },
		Logf: func(f string, a ...any) {
			lmu.Lock()
			logged = append(logged, f)
			lmu.Unlock()
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go g.Run(ctx)

	select {
	case name := <-got:
		if name != "MESSAGE_CREATE" {
			t.Errorf("event %q", name)
		}
	case <-ctx.Done():
		t.Fatal("no event arrived")
	}
	cancel()

	fg.mu.Lock()
	defer fg.mu.Unlock()
	want := []int{allIntents, allIntents &^ intentMessageContent, baseIntents}
	if len(fg.intents) != len(want) {
		t.Fatalf("identify intents: %v, want %v", fg.intents, want)
	}
	for i := range want {
		if fg.intents[i] != want[i] {
			t.Errorf("attempt %d asked for %d, want %d", i, fg.intents[i], want[i])
		}
	}
	if g.Granted.Load() {
		t.Error("message content was refused, Granted must be false")
	}
	lmu.Lock()
	defer lmu.Unlock()
	if len(logged) < 2 {
		t.Errorf("the lost intents must be said in the service log once each: %v", logged)
	}
}

func TestGatewayGivesUpOnARefusedToken(t *testing.T) {
	srv := httptest.NewServer(&fakeGateway{})
	defer srv.Close()
	g := &Gateway{Token: "wrong", URL: srv.URL}
	done := make(chan struct{})
	go func() { g.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a refused token must stop the reconnect loop")
	}
}
