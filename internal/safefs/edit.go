package safefs

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxEditSize is the largest config file the editor will touch.
const MaxEditSize = 256 << 10

// Editable reports whether a file name has an extension the editor allows.
func Editable(name string) bool { return editable[strings.ToLower(path.Ext(name))] }

// ValidText says whether data may be saved by the editor: valid UTF-8, no NUL
// bytes, not too large. Anything else is probably a binary file.
func ValidText(data []byte) bool {
	return len(data) <= MaxEditSize && utf8.Valid(data) && !strings.ContainsRune(string(data), 0)
}

// List returns editable regular files under the given sub-directories of the
// root, as root-relative names, sorted. It does not follow symlinks and stops
// at limit entries and a fixed depth.
func (f *FS) List(dirs []string, limit int) ([]string, error) {
	var out []string
	for _, d := range dirs {
		if err := check(d); err != nil {
			return nil, err
		}
		sub, err := f.root.OpenRoot(d)
		if err != nil {
			continue // a missing directory just has nothing to list
		}
		fsys := sub.FS()
		fs.WalkDir(fsys, ".", func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() {
				if strings.Count(p, "/") >= 4 {
					return fs.SkipDir
				}
				return nil
			}
			if e.Type().IsRegular() && Editable(e.Name()) {
				if info, err := e.Info(); err == nil && info.Size() <= MaxEditSize {
					out = append(out, d+"/"+p)
				}
			}
			if len(out) >= limit {
				return fs.SkipAll
			}
			return nil
		})
		sub.Close()
	}
	sort.Strings(out)
	return out, nil
}

// Replace overwrites an existing editable file. The old content is kept next
// to it as <name>.stokehold-bak, the new content is written to a temporary
// file with the old file's mode (and owner, via chown) and renamed into place,
// so a crash never leaves a half-written config. It refuses new files,
// symlinks, special files and non-text data.
func (f *FS) Replace(rel string, data []byte) error {
	if err := check(rel); err != nil {
		return err
	}
	if !Editable(rel) || !ValidText(data) {
		return fmt.Errorf("%w: %q", ErrDenied, rel)
	}
	old, err := f.root.Lstat(rel)
	if err != nil {
		return err
	}
	if !old.Mode().IsRegular() {
		return fmt.Errorf("%w: %q is not a regular file", ErrDenied, rel)
	}
	prev, err := f.ReadFile(rel, MaxEditSize)
	if err != nil {
		return err
	}
	if err := f.writeNew(rel+".stokehold-bak", prev, old); err != nil {
		return err
	}
	return f.writeNew(rel, data, old)
}

// writeNew writes data to a temp file beside name, copies mode and owner from
// like, and renames it over name.
func (f *FS) writeNew(name string, data []byte, like fs.FileInfo) error {
	tmp := name + ".tmp"
	f.root.Remove(tmp)
	file, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, like.Mode().Perm())
	if err != nil {
		return err
	}
	cleanup := func() { file.Close(); f.root.Remove(tmp) }
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		f.root.Remove(tmp)
		return err
	}
	if err := f.root.Chmod(tmp, like.Mode().Perm()); err != nil {
		f.root.Remove(tmp)
		return err
	}
	if err := chownLike(f.root, tmp, like); err != nil {
		f.root.Remove(tmp)
		return err
	}
	if err := f.root.Rename(tmp, name); err != nil {
		f.root.Remove(tmp)
		return err
	}
	return nil
}
