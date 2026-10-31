// Package licsrv is the licence server: it issues and stores licence keys and
// hands out short-lived signed tokens (docs/security-licensing.md).
package licsrv

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"stokehold/internal/license"
)

// NewKey returns a fresh licence key for the buyer and its normalised form.
// Only a hash of the normalised form is ever stored, so the key can be shown
// exactly once.
func NewKey() (display, norm string) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no entropy, no keys
	}
	p := base32Std(b)
	norm = p + license.Checksum(p)
	var parts []string
	for i := 0; i < len(norm); i += 4 {
		parts = append(parts, norm[i:min(i+4, len(norm))])
	}
	return license.KeyPrefix + "-" + strings.Join(parts, "-"), norm
}

func ParseKey(s string) (string, bool) { return license.ParseKey(s) }

func hashKey(norm string) string {
	sum := sha256.Sum256([]byte("stokehold-key-hash-v1\x00" + norm))
	return hex.EncodeToString(sum[:])
}
