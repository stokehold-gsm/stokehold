// Package entitlement is the panel side of licensing: it activates a key with
// the licence server, keeps a short-lived signed token fresh, and answers one
// question for the rest of the panel: how many servers may be added.
//
// Rule from docs/security-licensing.md: nothing here can stop a running game
// server. Any failure falls back to the Free limit.
package entitlement

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/pkg/license"
)

// FreeServers is the Free tier limit.
const FreeServers = 1

const (
	setKey      = "license_key"
	setToken    = "license_token"
	setInstance = "instance_key"
	setLastSeen = "license_last_seen"
)

// Server-side error codes, as returned by the licence server.
var (
	ErrNotConfigured = errors.New("licence checks are not configured on this panel")
	ErrBadKey        = license.ErrBadKey
	ErrInvalid       = errors.New("invalid_license")
	ErrExpired       = errors.New("license_expired")
	ErrRevoked       = errors.New("license_revoked")
	ErrBound         = errors.New("bound_elsewhere")
	ErrUnreachable   = errors.New("licence server unreachable")
)

type Settings interface {
	Setting(key string) (string, bool)
	SetSetting(key, value string) error
}

type Manager struct {
	S     Settings
	V     *license.Verifier // Roots must be set; LastSeen is managed here
	URL   string            // licence server base URL, empty means Free only
	HTTP  *http.Client
	Panel string // panel version, sent for information
	Now   func() time.Time

	mu      sync.Mutex
	loaded  bool
	lastErr string
}

type Status struct {
	Configured bool
	HasKey     bool
	Plan       string
	MaxServers int
	State      license.State
	ExpiresAt  int64
	LastError  string
}

//go:embed roots
var rootsFS embed.FS

// EmbeddedRoots returns the root public keys built into this binary.
func EmbeddedRoots() map[string]ed25519.PublicKey {
	out := map[string]ed25519.PublicKey{}
	entries, _ := rootsFS.ReadDir("roots")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".pem") {
			continue
		}
		b, err := rootsFS.ReadFile(path.Join("roots", e.Name()))
		if err != nil {
			continue
		}
		if pub, err := ParsePublicPEM(b); err == nil {
			out[strings.TrimSuffix(e.Name(), ".pem")] = pub
		}
	}
	return out
}

func ParsePublicPEM(b []byte) (ed25519.PublicKey, error) {
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return nil, errors.New("not a PEM public key")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an ed25519 key")
	}
	return pub, nil
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	if v, ok := m.S.Setting(setLastSeen); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > m.V.LastSeen {
			m.V.LastSeen = n
		}
	}
}

// instanceKey is this panel's own keypair. It is created on first use and
// never leaves the database.
func (m *Manager) instanceKey() (ed25519.PrivateKey, error) {
	if s, ok := m.S.Setting(setInstance); ok {
		if blk, _ := pem.Decode([]byte(s)); blk != nil {
			if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
				if pk, ok := k.(ed25519.PrivateKey); ok {
					return pk, nil
				}
			}
		}
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := m.S.SetSetting(setInstance, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))); err != nil {
		return nil, err
	}
	return priv, nil
}

func (m *Manager) configured() bool { return m.URL != "" && len(m.V.Roots) > 0 }

// Status evaluates the stored token. Anything wrong means Free.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status()
}

func (m *Manager) status() Status {
	m.load()
	st := Status{Configured: m.configured(), LastError: m.lastErr, MaxServers: FreeServers}
	_, st.HasKey = m.S.Setting(setKey)
	tok, ok := m.S.Setting(setToken)
	if !ok || !st.Configured {
		return st
	}
	priv, err := m.instanceKey()
	if err != nil {
		return st
	}
	inst := instanceID(priv)
	c, state, err := m.V.Check(tok, inst, m.now())
	m.S.SetSetting(setLastSeen, strconv.FormatInt(m.V.LastSeen, 10))
	if err != nil || state == license.Free {
		return st
	}
	st.State, st.Plan, st.ExpiresAt = state, c.Plan, c.EXP
	if c.MaxServers > 0 {
		st.MaxServers = c.MaxServers
	}
	return st
}

// Limit is how many servers may exist on this panel.
func (m *Manager) Limit() int { return m.Status().MaxServers }

type tokenResponse struct {
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
	Error   string `json:"error"`
}

// Activate checks a typed key, asks the licence server for a token, verifies
// that token locally, and only then stores the key and the token.
func (m *Manager) Activate(ctx context.Context, key string) error {
	norm, ok := license.ParseKey(key)
	if !ok {
		return ErrBadKey // caught by the checksum, no network needed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.configured() {
		return ErrNotConfigured
	}
	if err := m.fetch(ctx, norm); err != nil {
		m.lastErr = err.Error()
		return err
	}
	m.lastErr = ""
	return m.S.SetSetting(setKey, norm)
}

// Renew refreshes the token for the stored key. A failure keeps the old token.
func (m *Manager) Renew(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	norm, ok := m.S.Setting(setKey)
	if !ok || !m.configured() {
		return nil
	}
	if err := m.fetch(ctx, norm); err != nil {
		m.lastErr = err.Error()
		return err
	}
	m.lastErr = ""
	return nil
}

func (m *Manager) fetch(ctx context.Context, norm string) error {
	m.load()
	priv, err := m.instanceKey()
	if err != nil {
		return err
	}
	ts := m.now().Unix()
	inst, sig, err := license.SignRequest(priv, norm, ts)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"key": norm, "inst": inst, "ts": ts, "sig": sig, "panel": m.Panel})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.URL, "/")+"/v1/token", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&tr); err != nil {
		return fmt.Errorf("%w: bad answer (HTTP %d)", ErrUnreachable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		switch tr.Error {
		case "license_expired":
			return ErrExpired
		case "license_revoked":
			return ErrRevoked
		case "bound_elsewhere":
			return ErrBound
		case "rate_limited":
			return fmt.Errorf("%w: rate limited", ErrUnreachable)
		}
		return ErrInvalid
	}
	// Never store something we could not verify ourselves.
	_, state, err := m.V.Check(tr.Token, inst, m.now())
	if err != nil || state != license.Active {
		return fmt.Errorf("the licence server returned a token this panel does not trust (%v)", err)
	}
	if err := m.S.SetSetting(setToken, tr.Token); err != nil {
		return err
	}
	return m.S.SetSetting(setLastSeen, strconv.FormatInt(m.V.LastSeen, 10))
}

// RenewLoop renews once at start and then daily until ctx ends. Errors are
// recorded and shown on the licence page, never fatal.
func (m *Manager) RenewLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		m.Renew(rctx)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// instanceID is the public half of the instance key, base64, as the server sees it.
func instanceID(priv ed25519.PrivateKey) string {
	return base64.RawStdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}
