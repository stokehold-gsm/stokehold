// Package sched runs the schedules saved in the panel (backups, restarts,
// update checks) by sending ordinary requests to the helper.
package sched

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/lgsm"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

type Store interface {
	Due(now time.Time) ([]store.Schedule, error)
	MarkRun(id int64, at time.Time, ok bool, note string) error
	Audit(userID int64, action, detail string)
}

// HelperFunc sends a request to the helper of a node (0 is this machine).
type HelperFunc func(node int64, r helper.Request) (helper.Response, error)

type Runner struct {
	S      Store
	Helper HelperFunc
	Now    func() time.Time

	mu sync.Mutex // schedules run one at a time, so two game servers never restart together
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Tick runs every schedule that is due and returns how many ran. Each run is
// recorded even when it fails, so a broken server does not make the same
// schedule fire again on the next tick.
func (r *Runner) Tick() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	due, err := r.S.Due(now)
	if err != nil || r.Helper == nil {
		return 0
	}
	for _, x := range due {
		resp, err := r.Helper(x.NodeID, helper.Request{Op: "run", User: x.LinuxUser, Game: x.Game, Cmd: x.Cmd})
		ok, note := err == nil && resp.OK, "OK"
		switch {
		case err != nil:
			note = "helper: " + err.Error()
		case !resp.OK:
			note = summary(resp.Out, resp.Err)
		}
		r.S.MarkRun(x.ID, now, ok, note)
		result := "ok"
		if !ok {
			result = "failed"
		}
		r.S.Audit(0, "schedule.run", x.ServerName+" "+x.Cmd+" "+result)
	}
	return len(due)
}

// summary keeps the last line of a failed command's output, which is usually
// the reason, without colour codes and without newlines.
func summary(out, errText string) string {
	lines := strings.Split(strings.TrimSpace(strings.ToValidUTF8(lgsm.Clean(out), "")), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		last = errText
	}
	if last == "" {
		last = "failed"
	}
	return last
}

func (r *Runner) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Tick()
		}
	}
}
