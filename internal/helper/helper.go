// Package helper is the small root-side service from docs/architecture.md.
// The panel talks to it over a unix socket with a fixed set of operations.
// Every field is validated here again; the panel is not trusted.
package helper

import (
	"errors"
	"fmt"
	"regexp"

	"stokehold/internal/lgsm"
)

// Game users carry a prefix so the helper can never touch a real account
// (root, the admin, someone else's factorio user).
var (
	userRE = regexp.MustCompile(`^gs_[a-z0-9]{1,24}$`)
	// Shape check only; what may be installed is decided by PackageAllowed.
	pkgRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{1,60}(:i386)?$`)
)

const maxPackages = 20

type Request struct {
	Op       string   `json:"op"` // create-user | install | deps | run
	User     string   `json:"user"`
	Game     string   `json:"game,omitempty"`
	Cmd      string   `json:"cmd,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

type Response struct {
	OK  bool   `json:"ok"`
	Out string `json:"out,omitempty"`
	Err string `json:"err,omitempty"`
}

var ErrInvalid = errors.New("invalid request")

func Validate(r Request) error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrInvalid, what) }
	switch r.Op {
	case "create-user":
	case "install":
		if !lgsm.ValidName(r.Game) {
			return bad("game")
		}
	case "run":
		if !lgsm.ValidName(r.Game) || !lgsm.ValidCommand(r.Cmd) {
			return bad("game or command")
		}
	case "deps":
		if len(r.Packages) == 0 || len(r.Packages) > maxPackages {
			return bad("package count")
		}
		for _, p := range r.Packages {
			if !pkgRE.MatchString(p) || !PackageAllowed(p) {
				return bad("package not allowed")
			}
		}
		return nil // deps run as root without a game user
	default:
		return bad("op")
	}
	if !userRE.MatchString(r.User) {
		return bad("user")
	}
	return nil
}

// Runner runs one command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

// Handle turns a validated request into fixed command lines. Nothing from the
// request is ever interpolated into a shell string except values that passed
// the regexes above (lowercase letters and digits).
func Handle(r Request, run Runner) Response {
	if err := Validate(r); err != nil {
		return Response{Err: err.Error()}
	}
	var out string
	var err error
	switch r.Op {
	case "create-user":
		// useradd fails on an existing user, so we never modify one.
		if out, err = run("useradd", "--create-home", "--shell", "/bin/bash", "--user-group", r.User); err == nil {
			out, err = run("chmod", "700", "/home/"+r.User)
		}
	case "install":
		script := `cd "$HOME" && curl -fsSL -o linuxgsm.sh https://linuxgsm.sh && chmod +x linuxgsm.sh && ./linuxgsm.sh "$1" && ./"$1" auto-install`
		out, err = run("runuser", "-l", r.User, "-c", "sh -c '"+script+"' sh "+r.Game)
	case "run":
		out, err = run("runuser", "-l", r.User, "-c", "./"+r.Game+" "+r.Cmd)
	case "deps":
		args := append([]string{"install", "-y", "--no-install-recommends", "--"}, r.Packages...)
		out, err = run("apt-get", args...)
	}
	if err != nil {
		return Response{Out: out, Err: err.Error()}
	}
	return Response{OK: true, Out: out}
}
