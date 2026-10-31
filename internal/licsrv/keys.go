// Package licsrv is the licence server: it issues and stores licence keys and
// hands out short-lived signed tokens (docs/security-licensing.md).
package licsrv

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
)

const (
	keyPrefix  = "SKH"
	payloadLen = 26 // 16 random bytes in base32, no padding
	checkLen   = 2
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func checksum(payload string) string {
	sum := sha256.Sum256([]byte("stokehold-key-v1\x00" + payload))
	return b32.EncodeToString(sum[:])[:checkLen]
}

// NewKey returns a fresh licence key for the buyer and its normalised form.
// Only a hash of the normalised form is ever stored, so the key can be shown
// exactly once.
func NewKey() (display, norm string) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no entropy, no keys
	}
	p := b32.EncodeToString(b)
	norm = p + checksum(p)
	var parts []string
	for i := 0; i < len(norm); i += 4 {
		parts = append(parts, norm[i:min(i+4, len(norm))])
	}
	return keyPrefix + "-" + strings.Join(parts, "-"), norm
}

// ParseKey normalises what a user typed (case, dashes, spaces) and checks the
// checksum so typos are caught without touching the database.
func ParseKey(s string) (norm string, ok bool) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s))
	s = strings.TrimPrefix(s, keyPrefix)
	if len(s) != payloadLen+checkLen {
		return "", false
	}
	if _, err := b32.DecodeString(s[:payloadLen]); err != nil {
		return "", false
	}
	if checksum(s[:payloadLen]) != s[payloadLen:] {
		return "", false
	}
	return s, true
}

func hashKey(norm string) string {
	sum := sha256.Sum256([]byte("stokehold-key-hash-v1\x00" + norm))
	return hex.EncodeToString(sum[:])
}
