package licsrv

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"stokehold/internal/license"
	"stokehold/internal/ratelimit"
)

var (
	ErrInvalid = errors.New("invalid_license")
	ErrExpired = errors.New("license_expired")
	ErrRevoked = errors.New("license_revoked")
	ErrBound   = errors.New("bound_elsewhere")
)

const (
	maxClockSkew = 5 * time.Minute
)

type Server struct {
	Store      *Store
	Key        ed25519.PrivateKey // online signing key
	Cert       license.Cert       // issued by the offline root key
	TTL        time.Duration
	TrustProxy bool
	Now        func() time.Time

	limiter *ratelimit.Limiter
}

func NewServer(st *Store, key ed25519.PrivateKey, cert license.Cert) *Server {
	return &Server{Store: st, Key: key, Cert: cert, TTL: 7 * 24 * time.Hour, Now: time.Now, limiter: ratelimit.New(30, time.Minute)}
}

// SignRequest is kept here for the server tests and tools; panels use the
// same function from the license package.
func SignRequest(priv ed25519.PrivateKey, key string, ts int64) (inst, sig string, err error) {
	inst, sig, err = license.SignRequest(priv, key, ts)
	if errors.Is(err, license.ErrBadKey) {
		err = ErrInvalid
	}
	return
}

// Token answers an activation or renewal. Every failure that could be caused
// by guessing a key looks the same (ErrInvalid), so the endpoint reveals
// nothing about which keys exist.
func (s *Server) Token(key, inst string, ts int64, sig string) (string, int64, error) {
	norm, ok := ParseKey(key)
	if !ok {
		return "", 0, ErrInvalid
	}
	pub, err := base64.RawStdEncoding.DecodeString(inst)
	rawSig, err2 := base64.RawStdEncoding.DecodeString(sig)
	if err != nil || err2 != nil || len(pub) != ed25519.PublicKeySize ||
		!ed25519.Verify(ed25519.PublicKey(pub), license.RequestMessage(norm, ts, inst), rawSig) {
		return "", 0, ErrInvalid
	}
	now := s.Now()
	if d := now.Sub(time.Unix(ts, 0)); d > maxClockSkew || d < -maxClockSkew {
		return "", 0, ErrInvalid
	}
	lic, err := s.Store.ByKey(norm)
	if err != nil {
		return "", 0, ErrInvalid
	}
	if lic.Status == StatusRevoked {
		return "", 0, ErrRevoked
	}
	if !now.Before(time.Unix(lic.ExpiresAt, 0)) {
		return "", 0, ErrExpired
	}
	bound, err := s.Store.bind(lic, inst, ts)
	if err != nil {
		return "", 0, err
	}
	if !bound {
		// either another instance holds the licence, or the timestamp was replayed
		if lic.InstancePub != "" && lic.InstancePub != inst {
			return "", 0, ErrBound
		}
		return "", 0, ErrInvalid
	}
	exp := now.Add(s.TTL)
	if lim := time.Unix(lic.ExpiresAt, 0); lim.Before(exp) {
		exp = lim
	}
	jti := make([]byte, 8)
	rand.Read(jti)
	cert := s.Cert
	token := license.Sign(s.Key, license.Claims{
		KID: cert.KID, JTI: hex.EncodeToString(jti), IAT: now.Unix(), EXP: exp.Unix(),
		Inst: inst, Plan: lic.Plan, MaxServers: lic.MaxServers, Cert: &cert,
	})
	return token, exp.Unix(), nil
}

type tokenRequest struct {
	Key   string `json:"key"`
	Inst  string `json:"inst"`
	TS    int64  `json:"ts"`
	Sig   string `json:"sig"`
	Panel string `json:"panel,omitempty"` // version, informational
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /v1/token", s.tokenHTTP)
	return mux
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) tokenHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.Allow(s.clientIP(r), s.Now()) {
		reply(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	var req tokenRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	token, exp, err := s.Token(req.Key, req.Inst, req.TS, req.Sig)
	switch {
	case err == nil:
		reply(w, http.StatusOK, map[string]any{"token": token, "expires": exp})
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrExpired), errors.Is(err, ErrRevoked):
		reply(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrBound):
		reply(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		reply(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
	}
}
