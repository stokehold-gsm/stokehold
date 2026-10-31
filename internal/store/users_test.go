package store

import (
	"errors"
	"testing"
)

func TestLastOwnerCannotBeDeleted(t *testing.T) {
	s, _ := open(t)
	owner, _ := s.CreateUser("o@example.com", pw, RoleOwner)
	admin, _ := s.CreateUser("a@example.com", pw, RoleAdmin)
	if err := s.DeleteUser(owner); !errors.Is(err, ErrLastOwner) {
		t.Errorf("last owner: %v", err)
	}
	if err := s.DeleteUser(admin); err != nil {
		t.Errorf("admin: %v", err)
	}
	second, _ := s.CreateUser("o2@example.com", pw, RoleOwner)
	if err := s.DeleteUser(owner); err != nil {
		t.Errorf("one of two owners: %v", err)
	}
	if err := s.DeleteUser(second); !errors.Is(err, ErrLastOwner) {
		t.Errorf("now the last one: %v", err)
	}
	if err := s.DeleteUser(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestSetPasswordEndsOtherSessions(t *testing.T) {
	s, _ := open(t)
	id, _ := s.CreateUser("a@example.com", pw, RoleAdmin)
	keep, _, _ := s.CreateSession(id)
	other, _, _ := s.CreateSession(id)
	if err := s.SetPassword(id, "a brand new password", keep); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUser(keep); err != nil {
		t.Error("the session that changed the password must survive")
	}
	if _, _, err := s.SessionUser(other); !errors.Is(err, ErrNotFound) {
		t.Error("other sessions must end")
	}
	if _, err := s.CheckLogin("a@example.com", pw); err == nil {
		t.Error("the old password still works")
	}
	if _, err := s.CheckLogin("a@example.com", "a brand new password"); err != nil {
		t.Errorf("new password: %v", err)
	}
	if err := s.SetPassword(id, "short", ""); err == nil {
		t.Error("a weak password was accepted")
	}
	if err := s.SetPassword(id, "another long password", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUser(keep); !errors.Is(err, ErrNotFound) {
		t.Error("with no keep token every session ends")
	}
}

func TestDeletedUserSessionsGoToo(t *testing.T) {
	s, _ := open(t)
	s.CreateUser("o@example.com", pw, RoleOwner)
	id, _ := s.CreateUser("a@example.com", pw, RoleAdmin)
	tok, _, _ := s.CreateSession(id)
	s.DeleteUser(id)
	if _, _, err := s.SessionUser(tok); !errors.Is(err, ErrNotFound) {
		t.Error("a deleted user's session still works")
	}
}

func TestResetTwoFactor(t *testing.T) {
	s, _ := open(t)
	id, _ := s.CreateUser("a@example.com", pw, RoleAdmin)
	s.SetTOTP(id, "SECRET", true)
	s.ResetTwoFactor(id)
	if u, _ := s.UserByID(id); u.TOTPEnabled || u.TOTPSecret != "" {
		t.Errorf("%+v", u)
	}
}
