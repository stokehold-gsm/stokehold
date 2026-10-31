package store

import (
	"errors"

	"github.com/stokehold-gsm/stokehold/pkg/auth"
)

var ErrLastOwner = errors.New("cannot remove or demote the last owner")

type UserRow struct {
	ID          int64
	Email       string
	Role        string
	TOTPEnabled bool
}

func (s *Store) Users() ([]UserRow, error) {
	rows, err := s.db.Query(`select id, email, role, totp_enabled from users order by email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserRow
	for rows.Next() {
		var u UserRow
		var en int
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &en); err != nil {
			return nil, err
		}
		u.TOTPEnabled = en == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) ownerCount() (n int) {
	s.db.QueryRow(`select count(*) from users where role=?`, RoleOwner).Scan(&n)
	return
}

// DeleteUser removes an account and its sessions. The last owner can never be
// The last owner cannot be deleted.
func (s *Store) DeleteUser(id int64) error {
	u, err := s.UserByID(id)
	if err != nil {
		return err
	}
	if u.Role == RoleOwner && s.ownerCount() <= 1 {
		return ErrLastOwner
	}
	_, err = s.db.Exec(`delete from users where id=?`, id) // sessions go with it (cascade)
	return err
}

// SetPassword replaces a password and ends every session of that user, so a
// stolen session does not survive a reset. keepToken, if given, stays signed in.
func (s *Store) SetPassword(id int64, password, keepToken string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`update users set pass_hash=?, failed_logins=0, locked_until=0 where id=?`, hash, id); err != nil {
		return err
	}
	if keepToken == "" {
		_, err = tx.Exec(`delete from sessions where user_id=?`, id)
	} else {
		_, err = tx.Exec(`delete from sessions where user_id=? and token_hash<>?`, id, auth.HashToken(keepToken))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ResetTwoFactor turns a user's second factor off so they enrol again.
func (s *Store) ResetTwoFactor(id int64) error { return s.SetTOTP(id, "", false) }
