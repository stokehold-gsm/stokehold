package licsrv

import "encoding/base32"

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func base32Std(b []byte) string { return b32.EncodeToString(b) }
