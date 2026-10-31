package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

const pw = "a long enough password"

func TestMigrationsRunOnceAndReopen(t *testing.T) {
	s, path := open(t)
	if _, err := s.CreateUser("a@example.com", pw, RoleOwner); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if n, _ := s2.CountUsers(); n != 1 {
		t.Errorf("data lost across reopen: %d users", n)
	}
	var v int
	s2.db.QueryRow(`select v from schema_version`).Scan(&v)
	if v != len(migrations) {
		t.Errorf("schema version %d, want %d", v, len(migrations))
	}
}

func TestUsersEmailIsCaseInsensitiveAndUnique(t *testing.T) {
	s, _ := open(t)
	if _, err := s.CreateUser("Owner@Example.com", pw, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("owner@example.COM", pw, RoleAdmin); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := s.CreateUser("x@example.com", "short", RoleAdmin); err == nil {
		t.Error("weak password accepted")
	}
	if _, err := s.CreateUser("y@example.com", pw, "root"); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestLoginAndLockout(t *testing.T) {
	s, _ := open(t)
	s.CreateUser("a@example.com", pw, RoleOwner)
	if u, err := s.CheckLogin("A@example.com", pw); err != nil || u.Role != RoleOwner {
		t.Fatalf("good login: %v", err)
	}
	if _, err := s.CheckLogin("nobody@example.com", pw); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown email: %v", err)
	}
	for i := 0; i < maxFailures; i++ {
		if _, err := s.CheckLogin("a@example.com", "wrong password!"); !errors.Is(err, ErrNotFound) {
			t.Errorf("attempt %d: %v", i, err)
		}
	}
	// now locked: even the right password is refused
	if _, err := s.CheckLogin("a@example.com", pw); !errors.Is(err, ErrLocked) {
		t.Errorf("want ErrLocked, got %v", err)
	}
}

func TestSuccessfulLoginResetsFailures(t *testing.T) {
	s, _ := open(t)
	s.CreateUser("a@example.com", pw, RoleOwner)
	for i := 0; i < maxFailures-1; i++ {
		s.CheckLogin("a@example.com", "wrong password!")
	}
	if _, err := s.CheckLogin("a@example.com", pw); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFailures-1; i++ {
		s.CheckLogin("a@example.com", "wrong password!")
	}
	if _, err := s.CheckLogin("a@example.com", pw); err != nil {
		t.Errorf("counter was not reset by a good login: %v", err)
	}
}

func TestTOTPStepCannotBeReused(t *testing.T) {
	s, _ := open(t)
	id, _ := s.CreateUser("a@example.com", pw, RoleOwner)
	if !s.UseTOTPStep(id, 100) {
		t.Fatal("first use refused")
	}
	if s.UseTOTPStep(id, 100) || s.UseTOTPStep(id, 99) {
		t.Error("a spent or older step was accepted")
	}
	if !s.UseTOTPStep(id, 101) {
		t.Error("a newer step was refused")
	}
}

func TestSessions(t *testing.T) {
	s, _ := open(t)
	id, _ := s.CreateUser("a@example.com", pw, RoleAdmin)
	tok, csrf, err := s.CreateSession(id)
	if err != nil || tok == "" || csrf == "" {
		t.Fatal(err)
	}
	u, gotCSRF, err := s.SessionUser(tok)
	if err != nil || u.ID != id || gotCSRF != csrf {
		t.Fatalf("lookup: %v", err)
	}
	var stored string
	s.db.QueryRow(`select token_hash from sessions`).Scan(&stored)
	if stored == tok {
		t.Error("the raw token must not be stored")
	}
	if _, _, err := s.SessionUser(tok + "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bad token: %v", err)
	}
	s.db.Exec(`update sessions set expires_at=?`, time.Now().Add(-time.Minute).Unix())
	if _, _, err := s.SessionUser(tok); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session accepted: %v", err)
	}
	tok2, _, _ := s.CreateSession(id)
	s.DeleteSession(tok2)
	if _, _, err := s.SessionUser(tok2); !errors.Is(err, ErrNotFound) {
		t.Error("deleted session still works")
	}
}

func TestServersAndAudit(t *testing.T) {
	s, _ := open(t)
	if _, err := s.AddServer(Server{Name: "Factorio", Game: "fctrserver", LinuxUser: "gs_fctr", Dir: "/home/gs_fctr", Mode: "managed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServer(Server{Name: "factorio", Game: "x", LinuxUser: "y", Dir: "/z", Mode: "managed"}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate name: %v", err)
	}
	if _, err := s.AddServer(Server{Name: "q", Game: "x", LinuxUser: "y", Dir: "/z", Mode: "weird"}); err == nil {
		t.Error("bad mode accepted")
	}
	list, _ := s.Servers()
	if len(list) != 1 || list[0].Name != "Factorio" {
		t.Errorf("servers: %+v", list)
	}
	s.Audit(0, "login.fail", "x")
	if s.AuditCount("login.fail") != 1 {
		t.Error("audit row missing")
	}
}
