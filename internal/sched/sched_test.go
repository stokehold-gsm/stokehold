package sched

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

type rig struct {
	t     *testing.T
	st    *store.Store
	calls []helper.Request
	resp  helper.Response
	err   error
	now   time.Time
	r     *Runner
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	g := &rig{t: t, st: st, now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), resp: helper.Response{OK: true}}
	g.r = &Runner{S: st, Now: func() time.Time { return g.now }, Helper: func(q helper.Request) (helper.Response, error) {
		g.calls = append(g.calls, q)
		return g.resp, g.err
	}}
	return g
}

func (g *rig) server(name, user, game string) int64 {
	id, err := g.st.AddServer(store.Server{Name: name, Game: game, LinuxUser: user, Dir: "/home/" + user, Mode: "managed"})
	if err != nil {
		g.t.Fatal(err)
	}
	return id
}

func TestDueScheduleRunsThroughTheHelperOnce(t *testing.T) {
	g := newRig(t)
	srv := g.server("mc", "gs_mc", "mcserver")
	g.st.AddSchedule(srv, "backup", store.KindInterval, 6, g.now)
	if n := g.r.Tick(); n != 0 || len(g.calls) != 0 {
		t.Fatal("ran before its time")
	}
	g.now = g.now.Add(6 * time.Hour)
	if n := g.r.Tick(); n != 1 {
		t.Fatalf("ran %d", n)
	}
	want := helper.Request{Op: "run", User: "gs_mc", Game: "mcserver", Cmd: "backup"}
	if len(g.calls) != 1 || g.calls[0].User != want.User || g.calls[0].Cmd != want.Cmd || g.calls[0].Op != "run" || g.calls[0].Game != want.Game {
		t.Errorf("helper got %+v", g.calls)
	}
	if n := g.r.Tick(); n != 0 || len(g.calls) != 1 {
		t.Error("the same slot ran twice")
	}
	list, _ := g.st.SchedulesFor(srv)
	if !list[0].LastOK || list[0].LastNote != "OK" {
		t.Errorf("result not recorded: %+v", list[0])
	}
	if g.st.AuditCount("schedule.run") != 1 {
		t.Error("run not audited")
	}
}

func TestFailuresAreRecordedAndDoNotLoop(t *testing.T) {
	g := newRig(t)
	srv := g.server("mc", "gs_mc", "mcserver")
	g.st.AddSchedule(srv, "restart", store.KindInterval, 1, g.now)
	g.now = g.now.Add(time.Hour)
	g.resp = helper.Response{OK: false, Out: "\x1b[31mstarting\x1b[0m\nFAIL: Missing dependencies: openjdk-25-jre\n", Err: "exit status 1"}
	g.r.Tick()
	list, _ := g.st.SchedulesFor(srv)
	if list[0].LastOK || !strings.Contains(list[0].LastNote, "Missing dependencies") || strings.Contains(list[0].LastNote, "\x1b") || strings.Contains(list[0].LastNote, "\n") {
		t.Errorf("note: %q", list[0].LastNote)
	}
	g.r.Tick()
	if len(g.calls) != 1 {
		t.Error("a failed schedule fired again on the next tick")
	}
	g.now = g.now.Add(time.Hour)
	g.err = errors.New("dial unix: connection refused")
	g.r.Tick()
	list, _ = g.st.SchedulesFor(srv)
	if len(g.calls) != 2 || !strings.Contains(list[0].LastNote, "helper:") {
		t.Errorf("a missing helper must be recorded: %+v calls=%d", list[0], len(g.calls))
	}
}

func TestSeveralDueSchedulesRunOneByOneInOrder(t *testing.T) {
	g := newRig(t)
	a, b := g.server("a", "gs_a", "mcserver"), g.server("b", "gs_b", "vhserver")
	g.st.AddSchedule(a, "backup", store.KindInterval, 1, g.now)
	g.st.AddSchedule(b, "check-update", store.KindInterval, 1, g.now)
	g.now = g.now.Add(time.Hour)
	if n := g.r.Tick(); n != 2 {
		t.Fatalf("ran %d", n)
	}
	if g.calls[0].User != "gs_a" || g.calls[1].User != "gs_b" || g.calls[1].Cmd != "check-update" {
		t.Errorf("%+v", g.calls)
	}
}

func TestNoHelperMeansNothingRuns(t *testing.T) {
	g := newRig(t)
	srv := g.server("mc", "gs_mc", "mcserver")
	g.st.AddSchedule(srv, "backup", store.KindInterval, 1, g.now)
	g.now = g.now.Add(time.Hour)
	g.r.Helper = nil
	if n := g.r.Tick(); n != 0 {
		t.Errorf("ran %d without a helper", n)
	}
	// and nothing was marked as run, so it fires once the helper is back
	g.r.Helper = func(helper.Request) (helper.Response, error) { return helper.Response{OK: true}, nil }
	if n := g.r.Tick(); n != 1 {
		t.Errorf("after the helper returned: %d", n)
	}
}

func TestSummaryFallbacks(t *testing.T) {
	if s := summary("", "exit status 2"); s != "exit status 2" {
		t.Errorf("%q", s)
	}
	if s := summary("   \n", ""); s != "failed" {
		t.Errorf("%q", s)
	}
}
