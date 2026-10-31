package safefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// Limits of the file manager operations. A chunk is what one helper request
// carries (the socket reads at most 1 MiB per request, base64 included).
const (
	MaxChunk   = 512 << 10
	MaxFileMax = 2 << 30 // no single upload may grow a file beyond this
	UploadTmp  = ".stokehold-up"
)

// Names a file manager must not touch anywhere in a game user's home: login
// scripts run by `runuser -l`, and SSH keys.
var sensitive = map[string]bool{
	".ssh": true, ".bashrc": true, ".profile": true, ".bash_profile": true,
	".bash_logout": true, "authorized_keys": true,
}

type Entry struct {
	Name  string `json:"n"`
	Type  string `json:"t"` // d directory, f file, l symlink (never followed), o other
	Size  int64  `json:"s"`
	Mtime int64  `json:"m"`
}

// SetOwner makes everything the file manager creates belong to this account
// (the helper runs as root, the game user must be able to use the files).
func (f *FS) SetOwner(uid, gid int) { f.uid, f.gid, f.owned = uid, gid, true }

// checkFM is check plus the sensitive names; "." means the root and is
// allowed only where the caller says so.
func checkFM(rel string, allowRoot bool) error {
	if rel == "." && allowRoot {
		return nil
	}
	if err := check(rel); err != nil {
		return err
	}
	for _, part := range strings.Split(rel, "/") {
		if sensitive[strings.ToLower(part)] {
			return fmt.Errorf("%w: %q", ErrDenied, rel)
		}
	}
	return nil
}

func kind(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "l"
	case m.IsDir():
		return "d"
	case m.IsRegular():
		return "f"
	}
	return "o"
}

func entry(name string, fi fs.FileInfo) Entry {
	return Entry{Name: name, Type: kind(fi.Mode()), Size: fi.Size(), Mtime: fi.ModTime().Unix()}
}

// ListDir lists a directory, directories first, then by name. Symlinks are
// shown as such and never followed. more is true when limit cut the list.
func (f *FS) ListDir(rel string, limit int) (list []Entry, more bool, err error) {
	if err := checkFM(rel, true); err != nil {
		return nil, false, err
	}
	d, err := f.root.Open(rel)
	if err != nil {
		return nil, false, err
	}
	defer d.Close()
	if st, err := d.Stat(); err != nil || !st.IsDir() {
		return nil, false, fmt.Errorf("%w: not a directory", ErrDenied)
	}
	ents, err := d.ReadDir(-1)
	if err != nil {
		return nil, false, err
	}
	for _, e := range ents {
		if sensitive[strings.ToLower(e.Name())] {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, entry(e.Name(), fi))
	}
	sort.Slice(list, func(i, j int) bool {
		if (list[i].Type == "d") != (list[j].Type == "d") {
			return list[i].Type == "d"
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	if limit > 0 && len(list) > limit {
		list, more = list[:limit], true
	}
	return list, more, nil
}

func (f *FS) Stat(rel string) (Entry, error) {
	if err := checkFM(rel, true); err != nil {
		return Entry{}, err
	}
	fi, err := f.root.Lstat(rel)
	if err != nil {
		return Entry{}, err
	}
	name := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		name = rel[i+1:]
	}
	return entry(name, fi), nil
}

func (f *FS) own(rel string) {
	if f.owned {
		chownTo(f.root, rel, f.uid, f.gid)
	}
}

// Mkdir creates one directory; the parent must exist.
func (f *FS) Mkdir(rel string) error {
	if err := checkFM(rel, false); err != nil {
		return err
	}
	if err := f.root.Mkdir(rel, 0o755); err != nil {
		return err
	}
	f.own(rel)
	return nil
}

// PutChunk builds a file from sequential chunks in <rel>.stokehold-up and
// renames it into place with the last one, so a half-sent upload never
// replaces a good file. The target is never opened for writing: the rename
// swaps the name, so a hard link or symlink planted there is not written through.
func (f *FS) PutChunk(rel string, offset int64, data []byte, final bool) error {
	if err := checkFM(rel, false); err != nil {
		return err
	}
	if len(data) > MaxChunk || offset < 0 || offset+int64(len(data)) > MaxFileMax {
		return fmt.Errorf("%w: chunk", ErrDenied)
	}
	tmp := rel + UploadTmp
	var file *os.File
	var err error
	if offset == 0 {
		perm := fs.FileMode(0o644)
		if old, err := f.root.Lstat(rel); err == nil {
			if old.IsDir() {
				return fmt.Errorf("%w: %q is a directory", ErrDenied, rel)
			}
			if old.Mode().IsRegular() {
				perm = old.Mode().Perm()
			}
		}
		f.root.Remove(tmp)
		file, err = f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			f.own(tmp)
		}
	} else {
		file, err = f.root.OpenFile(tmp, os.O_WRONLY, 0)
		if err == nil {
			var st fs.FileInfo
			if st, err = file.Stat(); err == nil && (!st.Mode().IsRegular() || st.Size() != offset) {
				err = errors.New("upload chunk out of order")
			}
		}
	}
	if err != nil {
		if file != nil {
			file.Close()
		}
		return err
	}
	if _, err := file.WriteAt(data, offset); err != nil {
		file.Close()
		f.root.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		f.root.Remove(tmp)
		return err
	}
	if !final {
		return nil
	}
	if err := f.root.Rename(tmp, rel); err != nil {
		f.root.Remove(tmp)
		return err
	}
	return nil
}

// Put writes a whole stream the same way (used by downloads from the
// network); verify runs before the rename and a failure leaves the old file
// in place.
func (f *FS) Put(rel string, r io.Reader, max int64, verify func() error) error {
	if err := checkFM(rel, false); err != nil {
		return err
	}
	if old, err := f.root.Lstat(rel); err == nil && old.IsDir() {
		return fmt.Errorf("%w: %q is a directory", ErrDenied, rel)
	}
	tmp := rel + UploadTmp
	f.root.Remove(tmp)
	file, err := f.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	f.own(tmp)
	n, err := io.Copy(file, io.LimitReader(r, max+1))
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errors.New("file too large")
	}
	if err == nil && verify != nil {
		err = verify()
	}
	if err == nil {
		err = f.root.Rename(tmp, rel)
	}
	if err != nil {
		f.root.Remove(tmp)
	}
	return err
}

// ReadAt returns up to n bytes of a regular file starting at offset, and the
// file's size.
func (f *FS) ReadAt(rel string, offset, n int64) ([]byte, int64, error) {
	if err := checkFM(rel, false); err != nil {
		return nil, 0, err
	}
	if n < 0 || n > MaxChunk || offset < 0 {
		return nil, 0, fmt.Errorf("%w: range", ErrDenied)
	}
	file, err := f.root.Open(rel)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%w: %q is not a regular file", ErrDenied, rel)
	}
	buf := make([]byte, n)
	got, err := file.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	return buf[:got], st.Size(), nil
}

// Remove deletes a file, a symlink (not its target) or a directory tree.
func (f *FS) Remove(rel string) error {
	if err := checkFM(rel, false); err != nil {
		return err
	}
	fi, err := f.root.Lstat(rel)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return f.root.RemoveAll(rel)
	}
	return f.root.Remove(rel)
}

// Move renames within the root. It never overwrites and never moves a
// directory into itself.
func (f *FS) Move(from, to string) error {
	if err := checkFM(from, false); err != nil {
		return err
	}
	if err := checkFM(to, false); err != nil {
		return err
	}
	if from == to || strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("%w: %q into itself", ErrDenied, from)
	}
	if _, err := f.root.Lstat(to); err == nil {
		return fmt.Errorf("%w: %q already exists", ErrDenied, to)
	}
	return f.root.Rename(from, to)
}
