package licsrv

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("order already has a licence")
)

type License struct {
	ID          int64
	OrderRef    string
	Plan        string
	MaxServers  int
	ExpiresAt   int64
	Status      string
	Contact     string
	InstancePub string
	LastTS      int64
	CreatedAt   int64
	CreatedBy   string
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

var migrations = []string{`
create table licenses(
  id integer primary key,
  key_hash text not null unique,
  order_ref text not null unique,
  plan text not null,
  max_servers integer not null,
  expires_at integer not null,
  status text not null check (status in ('active','revoked')),
  contact text not null default '',
  instance_pub text not null default '',
  last_ts integer not null default 0,
  created_at integer not null,
  created_by text not null
);
create table audit(
  id integer primary key,
  ts integer not null,
  actor text not null,
  action text not null,
  detail text not null,
  prev_hash text not null,
  hash text not null
);`}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
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
		if _, err = s.db.Exec(`insert into schema_version(v) values(0)`); err != nil {
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

// audit appends to a hash chain inside the caller's transaction, so an action
// and its record succeed or fail together and a deleted or edited row breaks
// every later hash.
func audit(tx *sql.Tx, now time.Time, actor, action, detail string) error {
	var prev string
	err := tx.QueryRow(`select hash from audit order by id desc limit 1`).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	h := chainHash(prev, now.Unix(), actor, action, detail)
	_, err = tx.Exec(`insert into audit(ts, actor, action, detail, prev_hash, hash) values(?,?,?,?,?,?)`,
		now.Unix(), actor, action, detail, prev, h)
	return err
}

func chainHash(prev string, ts int64, actor, action, detail string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", prev, ts, actor, action, detail)))
	return hex.EncodeToString(sum[:])
}

// VerifyAudit recomputes the chain. It returns the id of the first row that
// does not fit, or 0 when everything is intact.
func (s *Store) VerifyAudit() (badID int64, err error) {
	rows, err := s.db.Query(`select id, ts, actor, action, detail, prev_hash, hash from audit order by id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var id, ts int64
		var actor, action, detail, ph, h string
		if err := rows.Scan(&id, &ts, &actor, &action, &detail, &ph, &h); err != nil {
			return 0, err
		}
		if ph != prev || chainHash(prev, ts, actor, action, detail) != h {
			return id, nil
		}
		prev = h
	}
	return 0, rows.Err()
}

type AuditRow struct {
	TS            int64
	Actor, Action string
	Detail        string
}

func (s *Store) Audit(limit int) ([]AuditRow, error) {
	rows, err := s.db.Query(`select ts, actor, action, detail from audit order by id desc limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.TS, &r.Actor, &r.Action, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Issue creates a licence for an order. The order reference is unique, so one
// Boosty or FunPay order can never produce two keys. The key is returned
// once and not stored.
func (s *Store) Issue(actor, orderRef, plan string, maxServers int, expires time.Time, contact string) (string, error) {
	orderRef = strings.TrimSpace(orderRef)
	if orderRef == "" || maxServers < 1 || !expires.After(s.now()) {
		return "", errors.New("order reference, a positive server limit and a future expiry are required")
	}
	display, norm := NewKey()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`insert into licenses(key_hash, order_ref, plan, max_servers, expires_at, status, contact, created_at, created_by)
		values(?,?,?,?,?,?,?,?,?)`, hashKey(norm), orderRef, plan, maxServers, expires.Unix(), StatusActive, contact, s.now().Unix(), actor)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: licenses.order_ref") {
			return "", ErrExists
		}
		return "", err
	}
	if err := audit(tx, s.now(), actor, "issue", fmt.Sprintf("order=%s plan=%s servers=%d until=%s", orderRef, plan, maxServers, expires.UTC().Format("2006-01-02"))); err != nil {
		return "", err
	}
	return display, tx.Commit()
}

const licCols = `id, order_ref, plan, max_servers, expires_at, status, contact, instance_pub, last_ts, created_at, created_by`

func scanLicense(r interface{ Scan(...any) error }) (*License, error) {
	var l License
	err := r.Scan(&l.ID, &l.OrderRef, &l.Plan, &l.MaxServers, &l.ExpiresAt, &l.Status, &l.Contact, &l.InstancePub, &l.LastTS, &l.CreatedAt, &l.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &l, err
}

func (s *Store) ByKey(norm string) (*License, error) {
	return scanLicense(s.db.QueryRow(`select `+licCols+` from licenses where key_hash=?`, hashKey(norm)))
}

func (s *Store) ByOrder(ref string) (*License, error) {
	return scanLicense(s.db.QueryRow(`select `+licCols+` from licenses where order_ref=?`, ref))
}

func (s *Store) List() ([]License, error) {
	rows, err := s.db.Query(`select ` + licCols + ` from licenses order by id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []License
	for rows.Next() {
		l, err := scanLicense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (s *Store) change(actor, action, detail, query string, args ...any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	if err := audit(tx, s.now(), actor, action, detail); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Revoke(actor, orderRef string) error {
	return s.change(actor, "revoke", "order="+orderRef, `update licenses set status=? where order_ref=?`, StatusRevoked, orderRef)
}

func (s *Store) Extend(actor, orderRef string, until time.Time) error {
	return s.change(actor, "extend", fmt.Sprintf("order=%s until=%s", orderRef, until.UTC().Format("2006-01-02")),
		`update licenses set expires_at=? where order_ref=?`, until.Unix(), orderRef)
}

// ResetBinding lets the licence move to a new panel instance.
func (s *Store) ResetBinding(actor, orderRef string) error {
	return s.change(actor, "reset-binding", "order="+orderRef, `update licenses set instance_pub='', last_ts=0 where order_ref=?`, orderRef)
}

// bind records a request from an instance. It succeeds only for a newer
// timestamp (no replays) and for the instance the licence is bound to, or any
// instance if it is still unbound. The first successful bind is audited.
func (s *Store) bind(l *License, inst string, ts int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`update licenses set last_ts=?, instance_pub=? where id=? and last_ts<? and (instance_pub='' or instance_pub=?)`,
		ts, inst, l.ID, ts, inst)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false, nil
	}
	if l.InstancePub == "" {
		if err := audit(tx, s.now(), "system", "bind", "order="+l.OrderRef); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
