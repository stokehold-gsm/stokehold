//go:build linux

package metrics

import "syscall"

func diskUsage(path string) (used, total uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		return
	}
	total = st.Blocks * uint64(st.Bsize)
	used = total - st.Bavail*uint64(st.Bsize)
	return
}
