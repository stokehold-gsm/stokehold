package web

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/lgsm"
)

type ServerStatus struct {
	State        string // STARTED, STOPPED, ... or unknown
	Players, Max int
	HasPlayers   bool
	At           time.Time
}

type statusCache struct {
	mu sync.Mutex
	m  map[int64]ServerStatus
}

func (c *statusCache) get(id int64) (ServerStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.m[id]
	return st, ok
}

func (c *statusCache) put(id int64, st ServerStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[int64]ServerStatus{}
	}
	c.m[id] = st
}

// Statuses returns a copy of the last known state of every server.
func (s *Server) Statuses() map[int64]ServerStatus {
	s.status.mu.Lock()
	defer s.status.mu.Unlock()
	out := make(map[int64]ServerStatus, len(s.status.m))
	for k, v := range s.status.m {
		out[k] = v
	}
	return out
}

// RefreshStatus asks every server for its details once. A machine that does
// not answer marks its servers unknown instead of keeping a stale "started".
// A change of state goes to Discord when the licence and the webhook allow it.
func (s *Server) RefreshStatus() {
	servers, err := s.Store.Servers()
	if err != nil {
		return
	}
	for _, sv := range servers {
		st := ServerStatus{State: "unknown", At: s.Now()}
		resp, err := s.callHelperOn(sv.NodeID, helper.Request{Op: "run", User: sv.LinuxUser, Game: sv.Game, Cmd: "details"})
		if err == nil {
			if state, perr := lgsm.ParseStatus(resp.Out); perr == nil {
				st.State = state
			}
			st.Players, st.Max, st.HasPlayers = lgsm.ParsePlayers(resp.Out)
		}
		prev, had := s.status.get(sv.ID)
		s.status.put(sv.ID, st)
		if had && prev.State != st.State {
			s.notify(sv.Name + ": " + strings.ToLower(prev.State) + " → " + strings.ToLower(st.State))
		}
	}
}

func (s *Server) StatusLoop(ctx context.Context, every time.Duration) {
	s.RefreshStatus()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RefreshStatus()
		}
	}
}
