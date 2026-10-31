//go:build !unix

package safefs

import (
	"io/fs"
	"os"
)

// No file owners to copy on this platform (tests and development only).
func chownLike(*os.Root, string, fs.FileInfo) error { return nil }
