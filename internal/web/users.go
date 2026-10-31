package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"stokehold/internal/auth"
	"stokehold/internal/store"
)

func (s *Server) denied(w http.ResponseWriter, p *page) {
	s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "forbidden")}, http.StatusForbidden)
}

func randomPassword() string {
	t, _ := auth.NewToken()
	return t[:20]
}

func validEmail(e string) bool {
	return len(e) >= 3 && len(e) <= 120 && strings.Count(e, "@") == 1 && !strings.ContainsAny(e, " \t\r\n<>\"'") &&
		!strings.HasPrefix(e, "@") && !strings.HasSuffix(e, "@")
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	p.UserRows, _ = s.Store.Users()
	s.render(w, "users", p, http.StatusOK)
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	email, role := strings.TrimSpace(r.PostFormValue("email")), r.PostFormValue("role")
	fail := func(key string, status int) {
		p.Error = tr(p.Lang, key)
		p.UserRows, _ = s.Store.Users()
		s.render(w, "users", p, status)
	}
	if !validEmail(email) || (role != store.RoleOwner && role != store.RoleAdmin && role != store.RoleViewer) {
		fail("user_invalid", http.StatusBadRequest)
		return
	}
	pw := randomPassword()
	id, err := s.Store.CreateUser(email, pw, role)
	if errors.Is(err, store.ErrExists) {
		fail("user_exists", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.Audit(p.User.ID, "user.create", email+" "+role)
	_ = id
	p.NewEmail, p.NewPassword = email, pw
	s.render(w, "newuser", p, http.StatusOK)
}

func (s *Server) userAction(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	target, uerr := s.Store.UserByID(id)
	if err != nil || uerr != nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	switch r.PathValue("action") {
	case "delete":
		if target.ID == p.User.ID {
			s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "user_self")}, http.StatusBadRequest)
			return
		}
		if err := s.Store.DeleteUser(target.ID); err != nil {
			key := "user_last_owner"
			if !errors.Is(err, store.ErrLastOwner) {
				key = "not_found"
			}
			s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, key)}, http.StatusBadRequest)
			return
		}
		s.Store.Audit(p.User.ID, "user.delete", target.Email)
		http.Redirect(w, r, "/users", http.StatusSeeOther)
	case "reset":
		pw := randomPassword()
		if err := s.Store.SetPassword(target.ID, pw, ""); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		s.Store.ResetTwoFactor(target.ID)
		s.Store.Audit(p.User.ID, "user.reset", target.Email)
		p.NewEmail, p.NewPassword = target.Email, pw
		// resetting your own password ends your own sessions, so sign in again
		s.render(w, "newuser", p, http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

// changePassword lets any signed-in user replace their own password. It asks
// for the current one and ends every other session.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, p *page) {
	fail := func(key string, status int) {
		p.Error = tr(p.Lang, key)
		s.render(w, "security", p, status)
	}
	if !auth.CheckPassword(r.PostFormValue("current"), p.User.PassHash) {
		fail("wrong_password", http.StatusForbidden)
		return
	}
	var keep string
	if c, err := r.Cookie(cookieName); err == nil {
		keep = c.Value
	}
	if err := s.Store.SetPassword(p.User.ID, r.PostFormValue("new"), keep); err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			fail("weak_password", http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.Audit(p.User.ID, "password.change", "")
	http.Redirect(w, r, "/security?ok=1", http.StatusSeeOther)
}
