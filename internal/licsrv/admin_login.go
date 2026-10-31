package licsrv

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/auth"
)

const (
	pendingTTL   = 5 * time.Minute
	pendingTries = 5
)

// pending holds a sign-in whose password is right and whose two-factor code is
// still to come, so the code goes into its own window and the password is not resent.
type pending struct {
	adminID int64
	expires time.Time
	tries   int
}

type pendingSet struct {
	mu sync.Mutex
	m  map[string]*pending
}

func (p *pendingSet) add(adminID int64, now time.Time) string {
	b := make([]byte, 24)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]*pending{}
	}
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
	p.m[tok] = &pending{adminID: adminID, expires: now.Add(pendingTTL)}
	return tok
}

// take returns the pending sign-in; a wrong guess counts and the entry dies after too many.
func (p *pendingSet) get(tok string, now time.Time) (*pending, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.m[tok]
	if !ok || now.After(v.expires) {
		delete(p.m, tok)
		return nil, false
	}
	return v, true
}

func (p *pendingSet) fail(tok string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.m[tok]; ok {
		if v.tries++; v.tries >= pendingTries {
			delete(p.m, tok)
		}
	}
}

func (p *pendingSet) done(tok string) {
	p.mu.Lock()
	delete(p.m, tok)
	p.mu.Unlock()
}

// loginCode is the second step: only the code, tied to the pending sign-in.
func (u *AdminUI) loginCode(w http.ResponseWriter, r *http.Request) {
	tok := r.PostFormValue("pending")
	p := &apage{Pending: tok}
	if !u.limiter.Allow(u.clientIP(r), u.Now()) {
		p.Error = "Слишком много попыток. Попробуйте позже."
		u.render(w, "code", p, http.StatusTooManyRequests)
		return
	}
	pd, ok := u.pend.get(tok, u.Now())
	if !ok {
		p.Error = "Вход устарел, начните заново."
		u.render(w, "login", &apage{Error: p.Error}, http.StatusUnauthorized)
		return
	}
	a, err := u.Store.AdminByID(pd.adminID)
	if err == nil {
		step, good := auth.VerifyTOTP(a.TOTPSecret, strings.TrimSpace(r.PostFormValue("code")), u.Now())
		if !good || !u.Store.AdminUseTOTPStep(a.ID, step) {
			err = ErrNotFound
		}
	}
	if err != nil {
		u.pend.fail(tok)
		u.Store.AdminAudit("-", "login.fail", a.nameOrDash())
		p.Error = "Код не подошёл."
		u.render(w, "code", p, http.StatusUnauthorized)
		return
	}
	u.pend.done(tok)
	u.startSession(w, r, a)
}

func (a *Admin) nameOrDash() string {
	if a == nil {
		return "-"
	}
	return a.Name
}
