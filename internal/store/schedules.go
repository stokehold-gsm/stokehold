package store

import (
	"errors"
	"time"
)

const (
	KindDaily    = "daily"    // value = minute of the day, UTC
	KindInterval = "interval" // value = hours
)

var scheduleCmds = map[string]bool{"backup": true, "restart": true, "check-update": true}

type Schedule struct {
	ID         int64
	ServerID   int64
	Cmd        string
	Kind       string
	Value      int
	Enabled    bool
	LastRun    int64
	LastOK     bool
	LastNote   string
	ServerName string
	LinuxUser  string
	Game       string
	NodeID     int64
}

func init() {
	migrations = append(migrations, `
create table schedules(
  id integer primary key,
  server_id integer not null references servers(id) on delete cascade,
  cmd text not null check (cmd in ('backup','restart','check-update')),
  kind text not null check (kind in ('daily','interval')),
  value integer not null,
  enabled integer not null default 1,
  last_run integer not null default 0,
  last_ok integer not null default 0,
  last_note text not null default '',
  created_at integer not null
);`)
}

var ErrBadSchedule = errors.New("invalid schedule")

func validSchedule(cmd, kind string, value int) bool {
	if !scheduleCmds[cmd] {
		return false
	}
	switch kind {
	case KindDaily:
		return value >= 0 && value < 24*60
	case KindInterval:
		return value >= 1 && value <= 24*7
	}
	return false
}

// AddSchedule creates a schedule. last_run starts at "now" so a schedule made
// at 15:00 for 03:00 does not fire at once for a slot that has already passed.
func (s *Store) AddSchedule(serverID int64, cmd, kind string, value int, now time.Time) (int64, error) {
	if !validSchedule(cmd, kind, value) {
		return 0, ErrBadSchedule
	}
	res, err := s.db.Exec(`insert into schedules(server_id, cmd, kind, value, last_run, created_at) values(?,?,?,?,?,?)`,
		serverID, cmd, kind, value, now.Unix(), now.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteSchedule(id int64) error {
	res, err := s.db.Exec(`delete from schedules where id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

const schedCols = `sc.id, sc.server_id, sc.cmd, sc.kind, sc.value, sc.enabled, sc.last_run, sc.last_ok, sc.last_note, sv.name, sv.linux_user, sv.game, sv.node_id`

func (s *Store) querySchedules(where string, args ...any) ([]Schedule, error) {
	rows, err := s.db.Query(`select `+schedCols+` from schedules sc join servers sv on sv.id=sc.server_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var x Schedule
		var en, ok int
		if err := rows.Scan(&x.ID, &x.ServerID, &x.Cmd, &x.Kind, &x.Value, &en, &x.LastRun, &ok, &x.LastNote, &x.ServerName, &x.LinuxUser, &x.Game, &x.NodeID); err != nil {
			return nil, err
		}
		x.Enabled, x.LastOK = en == 1, ok == 1
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SchedulesFor(serverID int64) ([]Schedule, error) {
	return s.querySchedules(`where sc.server_id=? order by sc.id`, serverID)
}

// Due returns enabled schedules whose time has come. A daily one is due when
// the latest slot at or before now is newer than its last run, so a panel that
// was down at 03:00 catches up once instead of skipping the day, and never
// runs the same slot twice.
func (s *Store) Due(now time.Time) ([]Schedule, error) {
	all, err := s.querySchedules(`where sc.enabled=1 order by sc.id`)
	if err != nil {
		return nil, err
	}
	var due []Schedule
	for _, x := range all {
		switch x.Kind {
		case KindDaily:
			if x.LastRun < latestSlot(now, x.Value).Unix() {
				due = append(due, x)
			}
		case KindInterval:
			if now.Unix() >= x.LastRun+int64(x.Value)*3600 {
				due = append(due, x)
			}
		}
	}
	return due, nil
}

// latestSlot is the most recent HH:MM (UTC) at or before now.
func latestSlot(now time.Time, minuteOfDay int) time.Time {
	u := now.UTC()
	slot := time.Date(u.Year(), u.Month(), u.Day(), minuteOfDay/60, minuteOfDay%60, 0, 0, time.UTC)
	if slot.After(u) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

func (s *Store) MarkRun(id int64, at time.Time, ok bool, note string) error {
	o := 0
	if ok {
		o = 1
	}
	if len(note) > 300 {
		note = note[:300]
	}
	_, err := s.db.Exec(`update schedules set last_run=?, last_ok=?, last_note=? where id=?`, at.Unix(), o, note, id)
	return err
}
