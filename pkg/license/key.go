package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// Licence keys and the request a panel signs are shared by the server and the
// panel, so both sides agree on the format.

const (
	KeyPrefix  = "SKH"
	PayloadLen = 26 // 16 random bytes in base32, no padding
	CheckLen   = 2
	reqLabel   = "stokehold-token-v1\x00"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

var ErrBadKey = errors.New("not a valid licence key")

// Checksum is the two-character tail of a key. It catches typos before any
// network request is made.
func Checksum(payload string) string {
	sum := sha256.Sum256([]byte("stokehold-key-v1\x00" + payload))
	return b32.EncodeToString(sum[:])[:CheckLen]
}

// ParseKey normalises what a user typed (case, dashes, spaces) and checks the checksum.
func ParseKey(s string) (norm string, ok bool) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s))
	s = strings.TrimPrefix(s, KeyPrefix)
	if len(s) != PayloadLen+CheckLen {
		return "", false
	}
	if _, err := b32.DecodeString(s[:PayloadLen]); err != nil {
		return "", false
	}
	if Checksum(s[:PayloadLen]) != s[PayloadLen:] {
		return "", false
	}
	return s, true
}

// RequestMessage is what an instance signs: the normalised key, the time and
// its own public key (base64).
func RequestMessage(norm string, ts int64, inst string) []byte {
	return []byte(reqLabel + norm + "\x00" + strconv.FormatInt(ts, 10) + "\x00" + inst)
}

// SignRequest returns the instance id (its public key) and the signature a
// panel sends to the licence server.
func SignRequest(priv ed25519.PrivateKey, key string, ts int64) (inst, sig string, err error) {
	norm, ok := ParseKey(key)
	if !ok {
		return "", "", ErrBadKey
	}
	inst = base64.RawStdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	sig = base64.RawStdEncoding.EncodeToString(ed25519.Sign(priv, RequestMessage(norm, ts, inst)))
	return inst, sig, nil
}
