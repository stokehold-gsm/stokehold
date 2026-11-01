package qrsvg

import (
	"strings"
	"testing"
)

func TestSVGDrawsATOTPURI(t *testing.T) {
	s := string(SVG("otpauth://totp/Stokehold%20GSM:owner@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Stokehold%20GSM"))
	if !strings.HasPrefix(s, "<svg") || strings.Count(s, "h1v1") < 100 || strings.Contains(s, "owner@example.com") {
		t.Fatalf("unexpected svg: %.120s", s)
	}
}
