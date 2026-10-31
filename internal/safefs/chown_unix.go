//go:build unix

package safefs

import (
	"io/fs"
	"os"
	"syscall"
)

// chownLike gives the new file the owner and group of the file it replaces,
// so a config edited by root stays owned by the game user.
func chownLike(root *os.Root, name string, like fs.FileInfo) error {
	st, ok := like.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return root.Chown(name, int(st.Uid), int(st.Gid))
}
