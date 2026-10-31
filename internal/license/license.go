// Package license verifies signed tokens issued by the license server
// (docs/security-licensing.md). It must never import the package that
// controls game servers: a licence failure may only downgrade to Free.
package license

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	Grace    = 30 * 24 * time.Hour
	maxSkew  = 5 * time.Minute
	tokenVer = 1
	domain   = "stokehold-license-v1\x00"
)

type State int

const (
	Free State = iota
	Active
	InGrace // token expired, still inside the grace window
)

var ErrInvalid = errors.New("invalid license token")
var ErrExpired = errors.New("license token expired")

type Claims struct {
	V          int    `json:"v"`
	KID        string `json:"kid"`
	JTI        string `json:"jti"`
	IAT        int64  `json:"iat"`
	EXP        int64  `json:"exp"`
	Inst       string `json:"inst"` // base64 of the instance public key
	Plan       string `json:"plan"`
	MaxServers int    `json:"max_servers"`
}

// Verifier holds the trusted signing keys. LastSeen must be persisted by the
// caller: it is the latest time we have observed, so winding the clock back
// does not extend a token.
type Verifier struct {
	Keys     map[string]ed25519.PublicKey
	Revoked  map[string]bool // revoked kids
	LastSeen int64
}

// Sign is used by the license server and by tests.
func Sign(priv ed25519.PrivateKey, c Claims) string {
	c.V = tokenVer
	payload, _ := json.Marshal(c)
	sig := ed25519.Sign(priv, append([]byte(domain), payload...))
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(sig)
}

// Check returns Free with an error for anything wrong; callers treat that as
// "run as Free", never as a reason to stop anything.
func (v *Verifier) Check(token, inst string, now time.Time) (Claims, State, error) {
	enc := base64.RawURLEncoding
	p, s, ok := strings.Cut(token, ".")
	if !ok {
		return Claims{}, Free, ErrInvalid
	}
	payload, err1 := enc.DecodeString(p)
	sig, err2 := enc.DecodeString(s)
	if err1 != nil || err2 != nil {
		return Claims{}, Free, ErrInvalid
	}
	var c Claims
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || c.V != tokenVer {
		return Claims{}, Free, ErrInvalid
	}
	// The kid is read before verification only to pick a key; nothing in
	// the claims is trusted until the signature checks out.
	key, ok := v.Keys[c.KID]
	if !ok || v.Revoked[c.KID] || !ed25519.Verify(key, append([]byte(domain), payload...), sig) {
		return Claims{}, Free, ErrInvalid
	}
	if c.Inst != inst || c.IAT > now.Add(maxSkew).Unix() {
		return Claims{}, Free, ErrInvalid
	}

	eff := max(now.Unix(), v.LastSeen)
	v.LastSeen = max(eff, c.IAT)
	switch {
	case eff <= c.EXP:
		return c, Active, nil
	case eff <= c.EXP+int64(Grace.Seconds()):
		return c, InGrace, nil
	}
	return Claims{}, Free, ErrExpired
}
