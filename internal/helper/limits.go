package helper

import "time"

const (
	maxRequest = 1 << 20          // write-file carries a config of up to 256 KB
	opTimeout  = 45 * time.Minute // game installs download gigabytes
)
