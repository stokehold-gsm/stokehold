package helper

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/user"
	"strconv"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/safefs"
)

// File manager operations. Request fields they use (all paths are relative to
// the game user's home, "." is the home itself where listed):
//
//	list-dir   Path                      Out: JSON {"list":[{n,t,s,m}],"more":bool}
//	stat       Path                      Out: JSON {n,t,s,m}
//	mkdir      Path
//	put        Path Offset Data Final    Data is base64, at most safefs.MaxChunk raw bytes;
//	                                     chunks arrive in order, Final renames into place
//	get        Path Offset Length        Out: base64 of up to Length (<= MaxChunk) bytes; use stat for the size
//	delete     Path                      file, symlink or whole directory tree
//	rename     Path To                   never overwrites
//	fetch      Path URL [SHA256|SHA1]    download from an allow-listed host into Path
//	http-get-json URL                    Out: body (<= 4 MiB) from an allow-listed host; needs no user
//	extract    Path To [Sub]             unpack a .zip/.tar.gz in the home into the folder To; Out: files written
const maxListEntries = 5000

// FileManager is implemented by OSFiles; tests of the handler use it directly.
type FileManager interface {
	ListDir(user, rel string) ([]safefs.Entry, bool, error)
	Stat(user, rel string) (safefs.Entry, error)
	Mkdir(user, rel string) error
	Put(user, rel string, offset int64, data []byte, final bool) error
	Get(user, rel string, offset, n int64) ([]byte, error)
	Delete(user, rel string) error
	Rename(user, from, to string) error
	Fetch(user, rel, url string, sums Sums) error
	Extract(user, archive, dest, sub string) (int, error)
}

type Sums struct{ SHA256, SHA1 string }

func (o OSFiles) openOwned(account string) (*safefs.FS, error) {
	f, err := o.open(account)
	if err != nil {
		return nil, err
	}
	if o.Home == nil {
		// the real thing: the files must belong to the game user, not to root
		if u, err := user.Lookup(account); err == nil {
			uid, e1 := strconv.Atoi(u.Uid)
			gid, e2 := strconv.Atoi(u.Gid)
			if e1 == nil && e2 == nil {
				f.SetOwner(uid, gid)
			}
		}
	}
	return f, nil
}

func (o OSFiles) ListDir(account, rel string) ([]safefs.Entry, bool, error) {
	f, err := o.openOwned(account)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	return f.ListDir(rel, maxListEntries)
}

func (o OSFiles) Stat(account, rel string) (safefs.Entry, error) {
	f, err := o.openOwned(account)
	if err != nil {
		return safefs.Entry{}, err
	}
	defer f.Close()
	return f.Stat(rel)
}

func (o OSFiles) Mkdir(account, rel string) error {
	f, err := o.openOwned(account)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Mkdir(rel)
}

func (o OSFiles) Put(account, rel string, offset int64, data []byte, final bool) error {
	f, err := o.openOwned(account)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.PutChunk(rel, offset, data, final)
}

func (o OSFiles) Get(account, rel string, offset, n int64) ([]byte, error) {
	f, err := o.openOwned(account)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, _, err := f.ReadAt(rel, offset, n)
	return b, err
}

func (o OSFiles) Delete(account, rel string) error {
	f, err := o.openOwned(account)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Remove(rel)
}

func (o OSFiles) Rename(account, from, to string) error {
	f, err := o.openOwned(account)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Move(from, to)
}

func (o OSFiles) Extract(account, archive, dest, sub string) (int, error) {
	f, err := o.openOwned(account)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Extract(archive, dest, sub)
}

func (o OSFiles) Fetch(account, rel, url string, sums Sums) error {
	f, err := o.openOwned(account)
	if err != nil {
		return err
	}
	defer f.Close()
	body, closeBody, err := fetchOpen(url)
	if err != nil {
		return err
	}
	defer closeBody()
	var h hash.Hash
	var want string
	switch {
	case sums.SHA256 != "":
		h, want = sha256.New(), strings.ToLower(sums.SHA256)
	case sums.SHA1 != "":
		h, want = sha1.New(), strings.ToLower(sums.SHA1)
	}
	r := io.Reader(body)
	if h != nil {
		r = io.TeeReader(body, h)
	}
	return f.Put(rel, r, maxFetch, func() error {
		if h != nil && hex.EncodeToString(h.Sum(nil)) != want {
			return errors.New("checksum mismatch")
		}
		return nil
	})
}

// fmPathOK is the shape check for file manager paths; the confinement itself
// is safefs, this only keeps junk out of the logs and the socket.
func fmPathOK(p string, allowRoot bool) bool {
	if p == "." {
		return allowRoot
	}
	return p != "" && len(p) <= 400 && !strings.ContainsAny(p, "\\\x00") && !strings.HasPrefix(p, "/") &&
		!strings.Contains(p, "..") && !strings.Contains(p, "//") && !strings.HasSuffix(p, "/")
}

func validateFM(r Request) error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrInvalid, what) }
	switch r.Op {
	case "list-dir", "stat":
		if !fmPathOK(r.Path, true) {
			return bad("path")
		}
	case "mkdir", "delete", "get", "put", "rename", "fetch", "extract":
		if !fmPathOK(r.Path, false) {
			return bad("path")
		}
	}
	switch r.Op {
	case "rename", "extract":
		if !fmPathOK(r.To, false) {
			return bad("target path")
		}
		if r.Op == "extract" && r.Sub != "" && !fmPathOK(r.Sub, false) {
			return bad("subfolder")
		}
	case "put":
		if r.Offset < 0 || len(r.Data) > base64.StdEncoding.EncodedLen(safefs.MaxChunk) {
			return bad("chunk")
		}
	case "get":
		if r.Offset < 0 || r.Length < 0 || r.Length > safefs.MaxChunk {
			return bad("range")
		}
	case "fetch", "http-get-json":
		if err := fetchURLOK(r.URL); err != nil {
			return bad(err.Error())
		}
		if r.Op == "fetch" {
			if (r.SHA256 != "" && !hexOf(r.SHA256, 64)) || (r.SHA1 != "" && !hexOf(r.SHA1, 40)) {
				return bad("checksum")
			}
		}
	}
	return nil
}

func hexOf(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func handleFM(r Request, files Files) Response {
	fm, ok := files.(FileManager)
	if !ok {
		return Response{Err: "file operations are not available"}
	}
	fail := func(err error) Response { return Response{Err: err.Error()} }
	switch r.Op {
	case "list-dir":
		list, more, err := fm.ListDir(r.User, r.Path)
		if err != nil {
			return fail(err)
		}
		b, _ := json.Marshal(struct {
			List []safefs.Entry `json:"list"`
			More bool           `json:"more"`
		}{list, more})
		return Response{OK: true, Out: string(b)}
	case "stat":
		e, err := fm.Stat(r.User, r.Path)
		if err != nil {
			return fail(err)
		}
		b, _ := json.Marshal(e)
		return Response{OK: true, Out: string(b)}
	case "mkdir":
		if err := fm.Mkdir(r.User, r.Path); err != nil {
			return fail(err)
		}
	case "put":
		data, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			return Response{Err: "bad chunk encoding"}
		}
		if err := fm.Put(r.User, r.Path, r.Offset, data, r.Final); err != nil {
			return fail(err)
		}
	case "get":
		b, err := fm.Get(r.User, r.Path, r.Offset, r.Length)
		if err != nil {
			return fail(err)
		}
		return Response{OK: true, Out: base64.StdEncoding.EncodeToString(b)}
	case "delete":
		if err := fm.Delete(r.User, r.Path); err != nil {
			return fail(err)
		}
	case "rename":
		if err := fm.Rename(r.User, r.Path, r.To); err != nil {
			return fail(err)
		}
	case "extract":
		n, err := fm.Extract(r.User, r.Path, r.To, r.Sub)
		if err != nil {
			return fail(err)
		}
		return Response{OK: true, Out: strconv.Itoa(n)}
	case "fetch":
		if err := fm.Fetch(r.User, r.Path, r.URL, Sums{SHA256: r.SHA256, SHA1: r.SHA1}); err != nil {
			return fail(err)
		}
	default:
		return Response{Err: "unknown file operation"}
	}
	return Response{OK: true}
}

func handleJSON(r Request) Response {
	body, err := fetchJSON(r.URL)
	if err != nil {
		return Response{Err: err.Error()}
	}
	return Response{OK: true, Out: body}
}
