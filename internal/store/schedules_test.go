package store

import (
	"errors"
	"testing"
	"time"
)

func at(h, m int, dayOffset int) time.Time {
	return time.Date(2026, 11, 10+dayOffset, h, m, 0, 0, time.UTC)
}

func withServer(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.AddServer(Server{Name: "x", Game: "mcserver", LinuxUser: "gs_x", Dir: "/home/gs_x", Mode: "managed"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDailyScheduleRunsOncePerSlotAndCatchesUp(t *testing.T) {
	s, _ := open(t)
	srv := withServer(t, s)
	created := at(15, 0, 0)
	id, err := s.AddSchedule(srv, "backup", KindDaily, 3*60, created) // 03:00 UTC
	if err != nil {
		t.Fatal(err)
	}
	due := func(now time.Time) bool { d, _ := s.Due(now); return len(d) == 1 }
	if due(at(15, 1, 0)) || due(at(23, 59, 0)) {
		t.Error("a slot that passed before the schedule existed must not fire")
	}
	if due(at(2, 59, 1)) {
		t.Error("fired before 03:00")
	}
	if !due(at(3, 0, 1)) {
		t.Fatal("not due at 03:00 the next day")
	}
	s.MarkRun(id, at(3, 0, 1), true, "ok")
	if due(at(3, 1, 1)) || due(at(20, 0, 1)) {
		t.Error("ran twice for one slot")
	}
	if !due(at(3, 0, 2)) {
		t.Error("not due the following day")
	}
	// the panel was down for three days: it runs once, not three times
	if !due(at(9, 0, 5)) {
		t.Fatal("missed slots must catch up")
	}
	s.MarkRun(id, at(9, 0, 5), true, "ok")
	if due(at(9, 1, 5)) {
		t.Error("catch-up ran more than once")
	}
}

func TestIntervalSchedule(t *testing.T) {
	s, _ := open(t)
	srv := withServer(t, s)
	id, _ := s.AddSchedule(srv, "restart", KindInterval, 6, at(10, 0, 0))
	due := func(now time.Time) bool { d, _ := s.Due(now); return len(d) == 1 }
	if due(at(15, 59, 0)) {
		t.Error("fired before 6 hours")
	}
	if !due(at(16, 0, 0)) {
		t.Error("not due after 6 hours")
	}
	s.MarkRun(id, at(16, 0, 0), false, "boom")
	if due(at(21, 59, 0)) {
		t.Error("a failed run must still start the next interval")
	}
	if !due(at(22, 0, 0)) {
		t.Error("not due after the next interval")
	}
	list, _ := s.SchedulesFor(srv)
	if len(list) != 1 || list[0].LastOK || list[0].LastNote != "boom" || list[0].ServerName != "x" {
		t.Errorf("%+v", list)
	}
}

func TestScheduleValidationAndCascade(t *testing.T) {
	s, _ := open(t)
	srv := withServer(t, s)
	for _, c := range []struct {
		cmd, kind string
		v         int
	}{
		{"stop", KindDaily, 60}, {"console", KindDaily, 60}, {"backup", "weekly", 1}, {"backup", KindDaily, -1},
		{"backup", KindDaily, 1440}, {"backup", KindInterval, 0}, {"backup", KindInterval, 169}, {"", KindDaily, 0},
	} {
		if _, err := s.AddSchedule(srv, c.cmd, c.kind, c.v, at(0, 0, 0)); !errors.Is(err, ErrBadSchedule) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	id, _ := s.AddSchedule(srv, "check-update", KindDaily, 0, at(0, 0, 0))
	if err := s.DeleteSchedule(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	s.AddSchedule(srv, "backup", KindInterval, 24, at(0, 0, 0))
	if err := s.DeleteSchedule(id); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.SchedulesFor(srv); len(list) != 1 {
		t.Errorf("after delete: %d", len(list))
	}
	s.db.Exec(`delete from servers where id=?`, srv)
	var n int
	s.db.QueryRow(`select count(*) from schedules`).Scan(&n)
	if n != 0 {
		t.Error("schedules must go with their server")
	}
}

func TestDisabledSchedulesNeverRun(t *testing.T) {
	s, _ := open(t)
	srv := withServer(t, s)
	id, _ := s.AddSchedule(srv, "restart", KindInterval, 1, at(0, 0, 0))
	s.db.Exec(`update schedules set enabled=0 where id=?`, id)
	if d, _ := s.Due(at(23, 0, 3)); len(d) != 0 {
		t.Error("a disabled schedule was due")
	}
}
