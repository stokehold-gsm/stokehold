package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"strconv"
	"time"
)

const certDomain = "stokehold-cert-v1\x00"

func certMessage(c Cert) []byte {
	return []byte(certDomain + c.Root + "\x00" + c.KID + "\x00" + c.Pub + "\x00" + strconv.FormatInt(c.NotAfter, 10))
}

// IssueCert is run where the root key lives (offline) to authorise an online
// signing key for a limited time.
func IssueCert(rootID string, root ed25519.PrivateKey, kid string, pub ed25519.PublicKey, notAfter time.Time) Cert {
	c := Cert{Root: rootID, KID: kid, Pub: base64.RawStdEncoding.EncodeToString(pub), NotAfter: notAfter.Unix()}
	c.Sig = base64.RawStdEncoding.EncodeToString(ed25519.Sign(root, certMessage(c)))
	return c
}

// signingKey returns the key that must have signed the token. With a
// certificate the chain root -> online key is verified here; the certificate
// must have been valid when the token was issued (IAT), so tokens outlive
// their certificate by design and revocation is handled by kid.
func (v *Verifier) signingKey(c Claims) (ed25519.PublicKey, bool) {
	if c.Cert == nil {
		key, ok := v.Keys[c.KID]
		return key, ok
	}
	cert := c.Cert
	root, ok := v.Roots[cert.Root]
	if !ok || cert.KID != c.KID || c.IAT > cert.NotAfter {
		return nil, false
	}
	sig, err := base64.RawStdEncoding.DecodeString(cert.Sig)
	if err != nil || !ed25519.Verify(root, certMessage(*cert), sig) {
		return nil, false
	}
	pub, err := base64.RawStdEncoding.DecodeString(cert.Pub)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(pub), true
}
