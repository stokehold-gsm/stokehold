// Package helper is the small root-side service from docs/architecture.md.
// The panel talks to it over a unix socket with a fixed set of operations.
// Every field is validated here again; the panel is not trusted.
package helper

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/lgsm"
	"github.com/stokehold-gsm/stokehold/internal/safefs"
)

// Game users carry a prefix so the helper can never touch a real account
// (root, the admin, someone else's factorio user).
var (
	userRE = regexp.MustCompile(`^gs_[a-z0-9]{1,24}$`)
	// Existing accounts the owner chose to manage (adopt mode). Shape only; the
	// real gate is the root-owned adopt list.
	adoptRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	// Shape check only; what may be installed is decided by PackageAllowed.
	pkgRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{1,60}(:i386)?$`)
)

const maxPackages = 64 // LinuxGSM lists run past 20 packages for some games

type Request struct {
	Op       string   `json:"op"` // create-user | install | deps | run | discover | files | read-file | write-file | file manager ops (fm.go) | fetch | http-get-json
	User     string   `json:"user"`
	Game     string   `json:"game,omitempty"`
	Cmd      string   `json:"cmd,omitempty"`
	Packages []string `json:"packages,omitempty"`
	Path     string   `json:"path,omitempty"`    // read-file, write-file: relative to the user's home
	Content  string   `json:"content,omitempty"` // write-file
	To       string   `json:"to,omitempty"`      // rename: the new path
	Data     string   `json:"data,omitempty"`    // put: one base64 chunk
	Offset   int64    `json:"offset,omitempty"`  // put, get
	Length   int64    `json:"length,omitempty"`  // get
	Final    bool     `json:"final,omitempty"`   // put: the last chunk
	URL      string   `json:"url,omitempty"`     // fetch, http-get-json
	SHA256   string   `json:"sha256,omitempty"`  // fetch: expected checksum
	SHA1     string   `json:"sha1,omitempty"`    // fetch: expected checksum
}

type Response struct {
	OK  bool   `json:"ok"`
	Out string `json:"out,omitempty"`
	Err string `json:"err,omitempty"`
}

var ErrInvalid = errors.New("invalid request")

// Adopted says whether root has listed an existing account for adopt mode.
type Adopted func(user string) bool

func Validate(r Request) error { return ValidateWith(r, nil) }

func ValidateWith(r Request, adopted Adopted) error {
	bad := func(what string) error { return fmt.Errorf("%w: %s", ErrInvalid, what) }
	switch r.Op {
	case "create-user":
	case "discover", "files":
	case "list-dir", "stat", "mkdir", "put", "get", "delete", "rename", "fetch":
		if err := validateFM(r); err != nil {
			return err
		}
	case "http-get-json":
		return validateFM(r) // downloads for the panel run as root without a game user
	case "read-file", "write-file":
		if len(r.Path) == 0 || len(r.Path) > 300 || !inTree(r.Path) || !safefs.Editable(r.Path) || strings.ContainsAny(r.Path, "\\\x00") || strings.Contains(r.Path, "..") {
			return bad("path")
		}
		if r.Op == "write-file" && len(r.Content) > safefs.MaxEditSize {
			return bad("content too large")
		}
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
	if userRE.MatchString(r.User) {
		return nil
	}
	// An adopted account may only be driven and inspected, never created or installed into.
	if r.Op != "create-user" && r.Op != "install" && adopted != nil && r.User != "root" && adoptRE.MatchString(r.User) && adopted(r.User) {
		return nil
	}
	return bad("user")
}

// Runner runs one command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

// Handle turns a validated request into fixed command lines. Nothing from the
// request is ever interpolated into a shell string except values that passed
// the regexes above (lowercase letters and digits).
func Handle(r Request, run Runner) Response { return HandleWith(r, run, nil) }

func HandleWith(r Request, run Runner, adopted Adopted) Response {
	return HandleFull(r, Backend{Run: run, Adopted: adopted})
}

// Backend bundles what the operations need. Files may be nil, in which case
// the config editor operations answer that they are unavailable.
type Backend struct {
	Run     Runner
	Adopted Adopted
	Files   Files
}

func HandleFull(r Request, b Backend) Response {
	if err := ValidateWith(r, b.Adopted); err != nil {
		return Response{Err: err.Error()}
	}
	run := b.Run
	switch r.Op {
	case "files", "read-file", "write-file":
		return handleFiles(r, b.Files)
	case "list-dir", "stat", "mkdir", "put", "get", "delete", "rename", "fetch":
		return handleFM(r, b.Files)
	case "http-get-json":
		return handleJSON(r)
	}
	var out string
	var err error
	switch r.Op {
	case "create-user":
		// gs_ is reserved for accounts this panel makes, so an existing one is
		// ours from an earlier try: reuse it untouched, so a failed install can be retried.
		if _, idErr := run("id", "-u", r.User); idErr == nil {
			return Response{OK: true, Out: "account already exists, reusing it"}
		}
		if out, err = run("useradd", "--create-home", "--shell", "/bin/bash", "--user-group", r.User); err == nil {
			out, err = run("chmod", "700", "/home/"+r.User)
		}
	case "install":
		script := `cd "$HOME" && curl -fsSL -o linuxgsm.sh https://linuxgsm.sh && chmod +x linuxgsm.sh && ./linuxgsm.sh "$1" && ./"$1" auto-install`
		out, err = run("runuser", "-l", r.User, "-c", "sh -c '"+script+"' sh "+r.Game)
	case "run":
		out, err = run("runuser", "-l", r.User, "-c", "./"+r.Game+" "+r.Cmd)
	case "discover":
		out, err = run("runuser", "-l", r.User, "-c", "ls -1 lgsm/config-lgsm")
		out = onlyServerNames(out)
	case "deps":
		args := append([]string{"install", "-y", "--no-install-recommends", "--"}, r.Packages...)
		out, err = run("apt-get", args...)
	}
	if err != nil {
		return Response{Out: out, Err: err.Error()}
	}
	return Response{OK: true, Out: out}
}
