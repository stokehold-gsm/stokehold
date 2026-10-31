package helper

import (
	"errors"
	"os/user"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/safefs"
)

// только эти поддеревья home, остальное недоступно
var allowedTrees = []string{"serverfiles", "lgsm/config-lgsm"}

// _default.cfg is overwritten by every LinuxGSM update; its own advice is to
// put changes in the instance config instead.
func isDefaultCfg(rel string) bool { return strings.HasSuffix(rel, "/_default.cfg") }

func inTree(rel string) bool {
	if isDefaultCfg(rel) {
		return false
	}
	for _, t := range allowedTrees {
		if strings.HasPrefix(rel, t+"/") && len(rel) > len(t)+1 {
			return true
		}
	}
	return false
}

// CanEdit says whether the config editor accepts this path (the file manager
// uses it to decide which files get an Edit link).
func CanEdit(rel string) bool {
	return inTree(rel) && safefs.Editable(rel) && !strings.Contains(rel, "..")
}

// Files is the config editor backend; tests use a fake.
type Files interface {
	List(user string) ([]string, error)
	Read(user, rel string) (string, error)
	Write(user, rel, content string) error
}

// OSFiles works on real home directories. Home can be replaced in tests.
type OSFiles struct {
	Home func(user string) (string, error)
}

func (o OSFiles) open(account string) (*safefs.FS, error) {
	home := o.Home
	if home == nil {
		home = func(u string) (string, error) {
			x, err := user.Lookup(u)
			if err != nil {
				return "", err
			}
			return x.HomeDir, nil
		}
	}
	dir, err := home(account)
	if err != nil {
		return nil, err
	}
	return safefs.Open(dir)
}

func (o OSFiles) List(account string) ([]string, error) {
	f, err := o.open(account)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	all, err := f.List(allowedTrees, 400)
	var out []string
	for _, p := range all {
		if inTree(p) {
			out = append(out, p)
		}
	}
	return out, err
}

func (o OSFiles) Read(account, rel string) (string, error) {
	if !inTree(rel) || !safefs.Editable(rel) {
		return "", safefs.ErrDenied
	}
	f, err := o.open(account)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := f.ReadFile(rel, safefs.MaxEditSize)
	if err != nil {
		return "", err
	}
	if !safefs.ValidText(b) {
		return "", errors.New("not a text file")
	}
	return string(b), nil
}

func (o OSFiles) Write(account, rel, content string) error {
	if !inTree(rel) || !safefs.Editable(rel) {
		return safefs.ErrDenied
	}
	f, err := o.open(account)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Replace(rel, []byte(content))
}

func handleFiles(r Request, files Files) Response {
	if files == nil {
		return Response{Err: "file operations are not available"}
	}
	switch r.Op {
	case "files":
		list, err := files.List(r.User)
		if err != nil {
			return Response{Err: err.Error()}
		}
		return Response{OK: true, Out: strings.Join(list, "\n")}
	case "read-file":
		s, err := files.Read(r.User, r.Path)
		if err != nil {
			return Response{Err: err.Error()}
		}
		return Response{OK: true, Out: s}
	case "write-file":
		if err := files.Write(r.User, r.Path, r.Content); err != nil {
			return Response{Err: err.Error()}
		}
		return Response{OK: true}
	}
	return Response{Err: "unknown file operation"}
}
