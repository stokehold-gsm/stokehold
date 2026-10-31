//go:build !linux

package metrics

import "errors"

func diskUsage(string) (uint64, uint64, error) {
	return 0, 0, errors.New("disk usage is read on Linux only")
}
