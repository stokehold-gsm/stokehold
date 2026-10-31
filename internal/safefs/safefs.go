// Package safefs is the only way the panel touches files inside a game server
// directory. Everything goes through os.Root, which refuses to leave the root
// even via symlinks (see docs/architecture.md, CVE-2024-27102).
package safefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

var ErrDenied = errors.New("path not allowed")

var editable = map[string]bool{
	".cfg": true, ".ini": true, ".json": true, ".txt": true, ".yaml": true,
	".yml": true, ".toml": true, ".properties": true, ".conf": true,
}

type FS struct{ root *os.Root }

func Open(dir string) (*FS, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &FS{root: r}, nil
}

func (f *FS) Close() error { return f.root.Close() }

// check accepts only clean, relative, slash-separated names.
func check(rel string) error {
	if strings.ContainsAny(rel, "\\\x00") || !fs.ValidPath(rel) || rel == "." {
		return fmt.Errorf("%w: %q", ErrDenied, rel)
	}
	return nil
}

func (f *FS) ReadFile(rel string, max int64) ([]byte, error) {
	if err := check(rel); err != nil {
		return nil, err
	}
	file, err := f.root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return nil, fmt.Errorf("%w: %q", ErrDenied, rel)
	}
	return io.ReadAll(io.LimitReader(file, max))
}
