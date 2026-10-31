// Package store is the panel's SQLite database: users, sessions, the audit
// log and the list of game servers.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"stokehold/internal/auth"
)

const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleViewer = "viewer"

	SessionTTL     = 12 * time.Hour
	maxFailures    = 5
	lockoutMinutes = 15
)

var (
	ErrNotFound = errors.New("not found")
	ErrLocked   = errors.New("account locked")
	ErrExists   = errors.New("already exists")
)

type Store struct{ db *sql.DB }

type User struct {
	ID          int64
	Email       string
	PassHash    string
	Role        string
	TOTPSecret  string
	TOTPEnabled bool
	TOTPLast    int64
}

type Server struct {
	ID        int64
	Name      string
	Game      string
	LinuxUser string
	Dir       string
	Mode      string // managed | adopted
}

// Each entry runs once, in order. Never edit a shipped migration, add a new one.
var migrations = []string{`
create table users(
  id integer primary key,
  email text not null unique collate nocase,
  pass_hash text not null,
  role text not null check (role in ('owner','admin','viewer')),
  totp_secret text not null default '',
  totp_enabled integer not null default 0,
  totp_last_step integer not null default 0,
  failed_logins integer not null default 0,
  locked_until integer not null default 0,
  created_at integer not null
);
create table sessions(
  id integer primary key,
  user_id integer not null references users(id) on delete cascade,
  token_hash text not null unique,
  csrf text not null,
  created_at integer not null,
  expires_at integer not null
);
create index sessions_expires on sessions(expires_at);
create table audit(
  id integer primary key,
  ts integer not null,
  user_id integer,
  action text not null,
  detail text not null default ''
);
create table servers(
  id integer primary key,
  name text not null unique collate nocase,
  game text not null,
  linux_user text not null,
  dir text not null,
  mode text not null check (mode in ('managed','adopted')),
  created_at integer not null
);`, `
create table settings(
  key text primary key,
  value text not null
);`}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer, and no surprises with pragmas per connection
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`create table if not exists schema_version(v integer not null)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRow(`select v from schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.Exec(`insert into schema_version(v) values(0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`update schema_version set v=?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() int64 { return time.Now().Unix() }

func (s *Store) CountUsers() (n int, err error) {
	err = s.db.QueryRow(`select count(*) from users`).Scan(&n)
	return
}

func (s *Store) CreateUser(email, password, role string) (int64, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`insert into users(email, pass_hash, role, created_at) values(?,?,?,?)`, email, hash, role, now())
	if err != nil {
		if isUnique(err) {
			return 0, ErrExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

const userCols = `id, email, pass_hash, role, totp_secret, totp_enabled, totp_last_step`

func scanUser(r interface{ Scan(...any) error }) (*User, error) {
	var u User
	var en int
	err := r.Scan(&u.ID, &u.Email, &u.PassHash, &u.Role, &u.TOTPSecret, &en, &u.TOTPLast)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	u.TOTPEnabled = en == 1
	return &u, err
}

func (s *Store) UserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`select `+userCols+` from users where id=?`, id))
}

// CheckLogin verifies email and password and keeps the failure counter. The
// same error is returned for an unknown email and a wrong password.
func (s *Store) CheckLogin(email, password string) (*User, error) {
	var lockedUntil int64
	row := s.db.QueryRow(`select `+userCols+`, locked_until from users where email=?`, email)
	var u User
	var en int
	err := row.Scan(&u.ID, &u.Email, &u.PassHash, &u.Role, &u.TOTPSecret, &en, &u.TOTPLast, &lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		auth.CheckPassword(password, dummyHash) // keep the timing similar to a real check
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.TOTPEnabled = en == 1
	if lockedUntil > now() {
		return nil, ErrLocked
	}
	if !auth.CheckPassword(password, u.PassHash) {
		s.db.Exec(`update users set failed_logins=failed_logins+1,
			locked_until=case when failed_logins+1>=? then ? else locked_until end where id=?`,
			maxFailures, now()+lockoutMinutes*60, u.ID)
		return nil, ErrNotFound
	}
	s.db.Exec(`update users set failed_logins=0, locked_until=0 where id=?`, u.ID)
	return &u, nil
}

// A fixed, valid argon2id hash of a random password, only used to equalise timing.
var dummyHash, _ = auth.HashPassword("timing-equaliser-not-a-real-password")

// UseTOTPStep records a step as spent; it fails if it was already used or is older.
func (s *Store) UseTOTPStep(userID, step int64) bool {
	res, err := s.db.Exec(`update users set totp_last_step=? where id=? and totp_last_step<?`, step, userID, step)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (s *Store) SetTOTP(userID int64, secret string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := s.db.Exec(`update users set totp_secret=?, totp_enabled=? where id=?`, secret, en, userID)
	return err
}

// CreateSession returns the cookie token and the CSRF token. Only hashes and
// the CSRF value are stored.
func (s *Store) CreateSession(userID int64) (token, csrf string, err error) {
	token, hash := auth.NewToken()
	csrf, _ = auth.NewToken()
	_, err = s.db.Exec(`insert into sessions(user_id, token_hash, csrf, created_at, expires_at) values(?,?,?,?,?)`,
		userID, hash, csrf, now(), now()+int64(SessionTTL.Seconds()))
	return
}

func (s *Store) SessionUser(token string) (*User, string, error) {
	row := s.db.QueryRow(`select u.id, u.email, u.pass_hash, u.role, u.totp_secret, u.totp_enabled, u.totp_last_step, s.csrf
		from sessions s join users u on u.id=s.user_id where s.token_hash=? and s.expires_at>?`, auth.HashToken(token), now())
	var u User
	var en int
	var csrf string
	err := row.Scan(&u.ID, &u.Email, &u.PassHash, &u.Role, &u.TOTPSecret, &en, &u.TOTPLast, &csrf)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	u.TOTPEnabled = en == 1
	return &u, csrf, err
}

func (s *Store) DeleteSession(token string) {
	s.db.Exec(`delete from sessions where token_hash=?`, auth.HashToken(token))
}

func (s *Store) PurgeExpired() { s.db.Exec(`delete from sessions where expires_at<=?`, now()) }

func (s *Store) Audit(userID int64, action, detail string) {
	var uid any
	if userID != 0 {
		uid = userID
	}
	s.db.Exec(`insert into audit(ts, user_id, action, detail) values(?,?,?,?)`, now(), uid, action, detail)
}

func (s *Store) AuditCount(action string) (n int) {
	s.db.QueryRow(`select count(*) from audit where action=?`, action).Scan(&n)
	return
}

func (s *Store) AddServer(sv Server) (int64, error) {
	res, err := s.db.Exec(`insert into servers(name, game, linux_user, dir, mode, created_at) values(?,?,?,?,?,?)`,
		sv.Name, sv.Game, sv.LinuxUser, sv.Dir, sv.Mode, now())
	if err != nil {
		if isUnique(err) {
			return 0, ErrExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Servers() ([]Server, error) {
	rows, err := s.db.Query(`select id, name, game, linux_user, dir, mode from servers order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var sv Server
		if err := rows.Scan(&sv.ID, &sv.Name, &sv.Game, &sv.LinuxUser, &sv.Dir, &sv.Mode); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}

// AuditRows returns the newest audit entries as "action: detail" strings.
func (s *Store) AuditRows(limit int) ([]string, error) {
	rows, err := s.db.Query(`select action, detail from audit order by id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, d string
		if err := rows.Scan(&a, &d); err != nil {
			return nil, err
		}
		out = append(out, a+": "+d)
	}
	return out, rows.Err()
}
