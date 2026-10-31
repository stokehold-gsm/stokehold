// Package sign signs and verifies release files with ed25519. The installer
// verifies with OpenSSL, so keys are exchanged as standard PEM (PKCS#8 for the
// private key, SubjectPublicKeyInfo for the public key) and signatures are the
// raw 64 bytes, which `openssl pkeyutl -verify -rawin` accepts.
package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

func GenerateKey() (privPEM, pubPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	pb, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pb}), nil
}

func ParsePrivate(p []byte) (ed25519.PrivateKey, error) {
	b, _ := pem.Decode(p)
	if b == nil || b.Type != "PRIVATE KEY" {
		return nil, errors.New("not a PEM private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	pk, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an ed25519 key")
	}
	return pk, nil
}

func ParsePublic(p []byte) (ed25519.PublicKey, error) {
	b, _ := pem.Decode(p)
	if b == nil || b.Type != "PUBLIC KEY" {
		return nil, errors.New("not a PEM public key")
	}
	k, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an ed25519 key")
	}
	return pub, nil
}

func Sign(priv ed25519.PrivateKey, data []byte) []byte { return ed25519.Sign(priv, data) }

func Verify(pub ed25519.PublicKey, data, sig []byte) bool {
	return len(sig) == ed25519.SignatureSize && ed25519.Verify(pub, data, sig)
}
