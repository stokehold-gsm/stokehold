package licsrv

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/auth"
)

const (
	RoleOwner    = "owner"
	RoleCurator  = "curator"
	RoleOperator = "operator"
	RoleIssuer   = RoleOperator // the old name of the operator role

	AdminSessionTTL = 8 * time.Hour
	IssuerDailyMax  = 20 // licences one issuer may create per 24 hours; the owner is exempt
	maxFailures     = 5
	lockoutSeconds  = 15 * 60
)

var (
	ErrAdminExists = errors.New("admin already exists")
	ErrLocked      = errors.New("account locked")
	ErrQuota       = errors.New("daily issuing limit reached")
	ErrForbidden   = errors.New("this role cannot do that")
)

type Admin struct {
	ID          int64
	Name        string
	PassHash    string
	Role        string
	TOTPSecret  string
	TOTPEnabled bool
	TOTPLast    int64
}

func init() {
	migrations = append(migrations, `
create table admin_users(
  id integer primary key,
  name text not null unique collate nocase,
  pass_hash text not null,
  role text not null check (role in ('owner','issuer')),
  totp_secret text not null default '',
  totp_enabled integer not null default 0,
  totp_last_step integer not null default 0,
  failed_logins integer not null default 0,
  locked_until integer not null default 0,
  created_at integer not null
);
create table admin_sessions(
  id integer primary key,
  admin_id integer not null references admin_users(id) on delete cascade,
  token_hash text not null unique,
  csrf text not null,
  expires_at integer not null
);`)
}

func (s *Store) AdminCreate(name, password, role string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`insert into admin_users(name, pass_hash, role, created_at) values(?,?,?,?)`, strings.TrimSpace(name), hash, role, s.now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrAdminExists
	}
	return err
}

func (s *Store) AdminCount() (n int) {
	s.db.QueryRow(`select count(*) from admin_users`).Scan(&n)
	return
}

const adminCols = `id, name, pass_hash, role, totp_secret, totp_enabled, totp_last_step`

func scanAdmin(r interface{ Scan(...any) error }, extra ...any) (*Admin, error) {
	var a Admin
	var en int
	err := r.Scan(append([]any{&a.ID, &a.Name, &a.PassHash, &a.Role, &a.TOTPSecret, &en, &a.TOTPLast}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	a.TOTPEnabled = en == 1
	return &a, err
}

// AdminLogin checks name and password and keeps the failure counter. Unknown
// names and wrong passwords return the same error.
func (s *Store) AdminLogin(name, password string) (*Admin, error) {
	var locked int64
	a, err := scanAdmin(s.db.QueryRow(`select `+adminCols+`, locked_until from admin_users where name=?`, strings.TrimSpace(name)), &locked)
	if errors.Is(err, ErrNotFound) {
		auth.CheckPassword(password, dummyHash) // similar timing for unknown names
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if locked > s.now().Unix() {
		return nil, ErrLocked
	}
	if !auth.CheckPassword(password, a.PassHash) {
		s.db.Exec(`update admin_users set failed_logins=failed_logins+1,
			locked_until=case when failed_logins+1>=? then ? else locked_until end where id=?`,
			maxFailures, s.now().Unix()+lockoutSeconds, a.ID)
		return nil, ErrNotFound
	}
	s.db.Exec(`update admin_users set failed_logins=0, locked_until=0 where id=?`, a.ID)
	return a, nil
}

var dummyHash, _ = auth.HashPassword("timing-equaliser-not-a-real-password")

func (s *Store) AdminByID(id int64) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`select `+adminCols+` from admin_users where id=?`, id))
}

func (s *Store) AdminUseTOTPStep(id, step int64) bool {
	res, err := s.db.Exec(`update admin_users set totp_last_step=? where id=? and totp_last_step<?`, step, id, step)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (s *Store) AdminSetTOTP(id int64, secret string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := s.db.Exec(`update admin_users set totp_secret=?, totp_enabled=? where id=?`, secret, en, id)
	return err
}

func (s *Store) AdminSession(adminID int64) (token, csrf string, err error) {
	token, hash := auth.NewToken()
	csrf, _ = auth.NewToken()
	_, err = s.db.Exec(`insert into admin_sessions(admin_id, token_hash, csrf, expires_at) values(?,?,?,?)`,
		adminID, hash, csrf, s.now().Add(AdminSessionTTL).Unix())
	return
}

func (s *Store) AdminBySession(token string) (*Admin, string, error) {
	var csrf string
	a, err := scanAdmin(s.db.QueryRow(`select a.id, a.name, a.pass_hash, a.role, a.totp_secret, a.totp_enabled, a.totp_last_step, s.csrf
		from admin_sessions s join admin_users a on a.id=s.admin_id where s.token_hash=? and s.expires_at>?`, auth.HashToken(token), s.now().Unix()), &csrf)
	if err != nil {
		return nil, "", err
	}
	return a, csrf, nil
}

func (s *Store) AdminDeleteSession(token string) {
	s.db.Exec(`delete from admin_sessions where token_hash=?`, auth.HashToken(token))
}

// IssuedSince counts the licences an actor created after the given time.
func (s *Store) IssuedSince(actor string, since time.Time) (n int) {
	s.db.QueryRow(`select count(*) from licenses where created_by=? and created_at>?`, actor, since.Unix()).Scan(&n)
	return
}

// IssueAs issues a licence on behalf of an admin, enforcing the daily limit
// for issuers.
func (s *Store) IssueAs(a *Admin, orderRef, plan string, days int, contact string) (string, error) {
	if a.Role == RoleCurator {
		return "", ErrForbidden
	}
	if a.Role != RoleOwner && s.IssuedSince(a.Name, s.now().Add(-24*time.Hour)) >= IssuerDailyMax {
		return "", ErrQuota
	}
	return s.Issue(a.Name, orderRef, plan, s.now().AddDate(0, 0, days), contact)
}

// AdminAudit writes an event that is not tied to a licence change, such as a login.
func (s *Store) AdminAudit(actor, action, detail string) {
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if audit(tx, s.now(), actor, action, detail) == nil {
		tx.Commit()
	}
}

// AdminResetTOTP is for the owner on the command line when a helper loses
// their authenticator: it turns two-factor off so they must set it up again.
func (s *Store) AdminResetTOTP(name string) error {
	res, err := s.db.Exec(`update admin_users set totp_secret='', totp_enabled=0, failed_logins=0, locked_until=0 where name=?`, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	s.AdminAudit("cli", "admin.reset-2fa", name)
	return nil
}

func init() {
	migrations = append(migrations, `alter table licenses add column features text not null default '';`)
}

// Shop staff: operators issue keys for paid orders, curators watch the
// operators and handle support cases. The CHECK on role cannot be altered in
// SQLite, so the table is rebuilt; sessions are dropped with it and admins sign in again.
func init() {
	migrations = append(migrations, `
create table admin_users_new(
  id integer primary key,
  name text not null unique collate nocase,
  pass_hash text not null,
  role text not null check (role in ('owner','curator','operator')),
  totp_secret text not null default '',
  totp_enabled integer not null default 0,
  totp_last_step integer not null default 0,
  failed_logins integer not null default 0,
  locked_until integer not null default 0,
  created_at integer not null
);
insert into admin_users_new select id, name, pass_hash, case role when 'issuer' then 'operator' else role end,
  totp_secret, totp_enabled, totp_last_step, failed_logins, locked_until, created_at from admin_users;
drop table admin_users;
alter table admin_users_new rename to admin_users;`)
}

// ValidRole reports whether name is a role an admin can hold.
func ValidRole(name string) bool {
	return name == RoleOwner || name == RoleCurator || name == RoleOperator
}

type Staff struct {
	Name        string
	Role        string
	TOTPEnabled bool
	Issued24h   int
	IssuedTotal int
}

// StaffList is the roster with how much each person has issued.
func (s *Store) StaffList() ([]Staff, error) {
	rows, err := s.db.Query(`select name, role, totp_enabled from admin_users order by role, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Staff
	for rows.Next() {
		var m Staff
		var en int
		if err := rows.Scan(&m.Name, &m.Role, &en); err != nil {
			return nil, err
		}
		m.TOTPEnabled = en == 1
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	since := s.now().Add(-24 * time.Hour)
	for i := range out {
		out[i].Issued24h = s.IssuedSince(out[i].Name, since)
		s.db.QueryRow(`select count(*) from licenses where created_by=?`, out[i].Name).Scan(&out[i].IssuedTotal)
	}
	return out, nil
}

// ListIssuedBy is an operator's own view: only what they issued.
func (s *Store) ListIssuedBy(actor string) ([]License, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []License
	for _, l := range all {
		if l.CreatedBy == actor {
			out = append(out, l)
		}
	}
	return out, nil
}

// AdminSetPassword replaces a password (the owner on the command line, after a
// leak or a loss) and unlocks the account.
func (s *Store) AdminSetPassword(name, password string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`update admin_users set pass_hash=?, failed_logins=0, locked_until=0 where name=?`, hash, strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	s.db.Exec(`delete from admin_sessions where admin_id=(select id from admin_users where name=?)`, strings.TrimSpace(name))
	s.AdminAudit("cli", "admin.reset-password", name)
	return nil
}
