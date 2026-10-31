package web

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stokehold-gsm/stokehold/internal/store"
	"github.com/stokehold-gsm/stokehold/pkg/auth"
)

var newPwRE = regexp.MustCompile(`<code>([A-Za-z0-9_-]{20})</code>`)

func (r *rig) createUser(tok, emailAddr, role string) (code int, password string) {
	rec := r.do("POST", "/users/create", url.Values{"csrf": {tok}, "email": {emailAddr}, "role": {role}}, nil)
	if m := newPwRE.FindStringSubmatch(rec.Body.String()); m != nil {
		password = m[1]
	}
	return rec.Code, password
}

func TestOwnerCreatesUsersWhoCanSignIn(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	code, pw := r.createUser(tok, "new@example.com", "admin")
	if code != 200 || pw == "" {
		t.Fatalf("create: %d %q", code, pw)
	}
	if r.st.AuditCount("user.create") != 1 {
		t.Error("not audited")
	}
	r.jar = nil
	if rec := r.do("POST", "/login", url.Values{"email": {"new@example.com"}, "password": {pw}}, nil); rec.Code != 303 {
		t.Errorf("the new user cannot sign in: %d", rec.Code)
	}
	if users, _ := r.st.Users(); len(users) != 2 {
		t.Errorf("users: %+v", users)
	}
}

func TestUserCreationIsValidated(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	tok := r.csrf()
	for _, c := range []struct{ email, role string }{
		{"", "admin"}, {"no-at-sign", "admin"}, {"a b@example.com", "admin"}, {"@example.com", "admin"},
		{"x@example.com", "root"}, {"x@example.com", ""}, {"x@@example.com", "viewer"},
	} {
		if code, _ := r.createUser(tok, c.email, c.role); code != 400 {
			t.Errorf("%+v: %d", c, code)
		}
	}
	if code, _ := r.createUser(tok, "ok@example.com", "viewer"); code != 200 {
		t.Fatalf("valid: %d", code)
	}
	if code, _ := r.createUser(tok, "OK@example.com", "viewer"); code != 409 {
		t.Errorf("duplicate (case-insensitive): %d", code)
	}
}

func TestOnlyOwnersManageUsers(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.st.CreateUser("admin@example.com", pass, store.RoleAdmin)
	r.loginAs("admin@example.com", pass)
	tok := r.csrf()
	if rec := r.do("GET", "/users", nil, nil); rec.Code != 403 {
		t.Errorf("admin on /users: %d", rec.Code)
	}
	if code, _ := r.createUser(tok, "x@example.com", "owner"); code != 403 {
		t.Errorf("admin creating an owner: %d", code)
	}
	for _, act := range []string{"delete", "reset"} {
		if rec := r.do("POST", "/users/1/"+act, url.Values{"csrf": {tok}}, nil); rec.Code != 403 {
			t.Errorf("admin %s: %d", act, rec.Code)
		}
	}
	if body := r.do("GET", "/", nil, nil).Body.String(); strings.Contains(body, `href="/users"`) {
		t.Error("the Users link must not be shown to non-owners")
	}
	if u, _ := r.st.UserByID(1); u == nil {
		t.Error("the owner was removed by an admin")
	}
}

func TestResetGivesANewPasswordAndEndsSessionsAndTwoFactor(t *testing.T) {
	r := newRig(t)
	r.setup()
	id, _ := r.st.CreateUser("victim@example.com", pass, store.RoleAdmin)
	r.st.SetTOTP(id, auth.NewTOTPSecret(), true)
	old, _, _ := r.st.CreateSession(id)
	r.login("")
	tok := r.csrf()
	rec := r.do("POST", "/users/2/reset", url.Values{"csrf": {tok}}, nil)
	m := newPwRE.FindStringSubmatch(rec.Body.String())
	if rec.Code != 200 || m == nil {
		t.Fatalf("reset: %d", rec.Code)
	}
	if _, _, err := r.st.SessionUser(old); err == nil {
		t.Error("an old session survived the reset")
	}
	if u, _ := r.st.UserByID(id); u.TOTPEnabled {
		t.Error("2FA was not cleared")
	}
	r.jar = nil
	if rec := r.do("POST", "/login", url.Values{"email": {"victim@example.com"}, "password": {pass}}, nil); rec.Code != 401 {
		t.Errorf("the old password still works: %d", rec.Code)
	}
	if rec := r.do("POST", "/login", url.Values{"email": {"victim@example.com"}, "password": {m[1]}}, nil); rec.Code != 303 {
		t.Errorf("the new password does not work: %d", rec.Code)
	}
}

func TestDeleteRulesProtectTheOwner(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	r.login("")
	tok := r.csrf()
	if rec := r.do("POST", "/users/1/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 400 {
		t.Errorf("deleting yourself: %d", rec.Code)
	}
	if rec := r.do("POST", "/users/2/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 303 {
		t.Errorf("deleting a viewer: %d", rec.Code)
	}
	if rec := r.do("POST", "/users/99/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 404 {
		t.Errorf("unknown user: %d", rec.Code)
	}
	if rec := r.do("POST", "/users/abc/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
	if rec := r.do("POST", "/users/1/explode", url.Values{"csrf": {tok}}, nil); rec.Code != 404 {
		t.Errorf("unknown action: %d", rec.Code)
	}
	// a second owner makes the first deletable, but not by themself
	r.st.CreateUser("owner2@example.com", pass, store.RoleOwner)
	r.loginAs("owner2@example.com", pass)
	tok = r.csrf()
	if rec := r.do("POST", "/users/1/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 303 {
		t.Errorf("one of two owners: %d", rec.Code)
	}
	if rec := r.do("POST", "/users/2/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 400 {
		t.Errorf("the last owner deleting themself: %d", rec.Code)
	}
}

func TestChangingYourOwnPassword(t *testing.T) {
	r := newRig(t)
	r.setup()
	other, _, _ := r.st.CreateSession(1) // a session on another device
	r.login("")
	tok := r.csrf()
	form := func(cur, nw string) url.Values { return url.Values{"csrf": {tok}, "current": {cur}, "new": {nw}} }
	if rec := r.do("POST", "/security/password", form("not my password", "a brand new password"), nil); rec.Code != 403 {
		t.Errorf("wrong current password: %d", rec.Code)
	}
	if rec := r.do("POST", "/security/password", form(pass, "short"), nil); rec.Code != 400 {
		t.Errorf("weak new password: %d", rec.Code)
	}
	if rec := r.do("POST", "/security/password", form(pass, "a brand new password"), nil); rec.Code != 303 {
		t.Fatalf("change: %d", rec.Code)
	}
	if rec := r.do("GET", "/security?ok=1", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Password changed") {
		t.Errorf("this session must stay signed in and confirm: %d", rec.Code)
	}
	if _, _, err := r.st.SessionUser(other); err == nil {
		t.Error("another device's session survived the password change")
	}
	r.jar = nil
	if rec := r.login(""); rec.Code != 401 {
		t.Errorf("old password after the change: %d", rec.Code)
	}
}
