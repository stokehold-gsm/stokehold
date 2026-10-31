package store

import (
	"database/sql"
	"errors"
)

// Setting and SetSetting hold small key/value state such as the instance key
// and the current licence token.
func (s *Store) Setting(key string) (string, bool) {
	var v string
	err := s.db.QueryRow(`select value from settings where key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return "", false
	}
	return v, true
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`insert into settings(key, value) values(?,?) on conflict(key) do update set value=excluded.value`, key, value)
	return err
}
