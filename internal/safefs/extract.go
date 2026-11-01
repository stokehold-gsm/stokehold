package safefs

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

// Limits of one extraction, against archive bombs.
const (
	MaxExtractBytes int64 = 2 << 30
	MaxExtractFiles       = 20000
)

// Extract unpacks a .zip, .tar.gz or .tgz that lies inside the root into the
// directory dest (created when missing). With sub set, only entries under that
// top folder are taken and the folder itself is dropped from their names.
// An entry that would leave dest (absolute, "..") makes the whole archive
// refused; symlinks, devices and the like are skipped, never created. Files
// are written the same way Put writes them, so a planted link is not written
// through. It returns the number of files written.
func (f *FS) Extract(archive, dest, sub string) (int, error) {
	if err := checkFM(archive, false); err != nil {
		return 0, err
	}
	if err := checkFM(dest, false); err != nil {
		return 0, err
	}
	sub = strings.Trim(sub, "/")
	if sub != "" && !fs.ValidPath(sub) {
		return 0, fmt.Errorf("%w: sub %q", ErrDenied, sub)
	}
	file, err := f.root.Open(archive)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !st.Mode().IsRegular() {
		return 0, fmt.Errorf("%w: %q is not a regular file", ErrDenied, archive)
	}
	x := &extractor{f: f, dest: dest, sub: sub, left: MaxExtractBytes}
	if err := f.mkdirAll(dest); err != nil {
		return 0, err
	}
	low := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(low, ".zip"):
		zr, err := zip.NewReader(file, st.Size())
		if err != nil {
			return 0, err
		}
		for _, e := range zr.File {
			if err := x.entry(e.Name, e.Mode(), func() (io.ReadCloser, error) { return e.Open() }); err != nil {
				return x.files, err
			}
		}
	case strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz"):
		gz, err := gzip.NewReader(file)
		if err != nil {
			return 0, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return x.files, err
			}
			mode := h.FileInfo().Mode()
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				mode = fs.ModeSymlink // anything but a file or a directory is skipped
			}
			if err := x.entry(h.Name, mode, func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }); err != nil {
				return x.files, err
			}
		}
	default:
		return 0, fmt.Errorf("%w: only .zip, .tar.gz and .tgz can be unpacked", ErrDenied)
	}
	return x.files, nil
}

type extractor struct {
	f         *FS
	dest, sub string
	left      int64
	files     int
}

func (x *extractor) entry(name string, mode fs.FileMode, open func() (io.ReadCloser, error)) error {
	name = strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
	isDir := strings.HasSuffix(name, "/") || mode.IsDir()
	name = strings.TrimSuffix(name, "/")
	if name == "" || name == "." {
		return nil
	}
	if strings.HasPrefix(name, "/") || path.Clean(name) != name || !fs.ValidPath(name) {
		return fmt.Errorf("%w: archive entry %q leaves the target", ErrDenied, name)
	}
	if x.sub != "" {
		if name == x.sub {
			return nil
		}
		rest, ok := strings.CutPrefix(name, x.sub+"/")
		if !ok {
			return nil
		}
		name = rest
	}
	if mode&(fs.ModeSymlink|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeCharDevice|fs.ModeIrregular) != 0 {
		return nil
	}
	target := x.dest + "/" + name
	if err := checkFM(target, false); err != nil {
		return err
	}
	if isDir {
		return x.f.mkdirAll(target)
	}
	if x.files++; x.files > MaxExtractFiles {
		return fmt.Errorf("%w: more than %d files", ErrDenied, MaxExtractFiles)
	}
	if err := x.f.mkdirAll(path.Dir(target)); err != nil {
		return err
	}
	rc, err := open()
	if err != nil {
		return err
	}
	defer rc.Close()
	cr := &countReader{r: rc}
	if err := x.f.Put(target, cr, x.left, nil); err != nil {
		return err
	}
	x.left -= cr.n
	if mode.Perm()&0o111 != 0 {
		x.f.root.Chmod(target, 0o755)
	}
	return nil
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// mkdirAll creates rel and its missing parents inside the root, owned like
// everything else the file manager makes. An existing non-directory refuses.
func (f *FS) mkdirAll(rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(rel, "/") {
		if cur == "" {
			cur = part
		} else {
			cur += "/" + part
		}
		fi, err := f.root.Lstat(cur)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%w: %q is not a directory", ErrDenied, cur)
			}
			continue
		}
		if err := f.root.Mkdir(cur, 0o755); err != nil {
			return err
		}
		f.own(cur)
	}
	return nil
}
