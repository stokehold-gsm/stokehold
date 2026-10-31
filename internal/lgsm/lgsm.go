// Package lgsm drives an installed LinuxGSM script. Only whitelisted commands
// are run, never through a shell, and only for names that look like LinuxGSM
// scripts.
package lgsm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nameRE  = regexp.MustCompile(`^[a-z0-9]+server$`)
	ansiRE  = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	allowed = map[string]bool{
		"start": true, "stop": true, "restart": true, "details": true,
		"monitor": true, "check-update": true, "update": true, "backup": true,
	}
	statusRE = regexp.MustCompile(`(?m)^Status:\s+(\w+)`)
	fieldRE  = regexp.MustCompile(`(?m)^\* (Local build|Remote build|Branch): (.+)$`)
)

var ErrNotAllowed = errors.New("command not allowed")

type Server struct{ Name, Dir string }

func Clean(s string) string { return ansiRE.ReplaceAllString(s, "") }

// Discover finds LinuxGSM scripts directly in home (adopt mode, read-only).
// Symlinked scripts are skipped: we only adopt what really lives in home.
func Discover(home string) ([]Server, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	var found []Server
	for _, e := range entries {
		if !nameRE.MatchString(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Mode().Perm()&0o100 == 0 {
			continue
		}
		cfg := filepath.Join(home, "lgsm", "config-lgsm", e.Name())
		if st, err := os.Lstat(cfg); err == nil && st.IsDir() {
			found = append(found, Server{Name: e.Name(), Dir: home})
		}
	}
	return found, nil
}

// Run executes one whitelisted command and returns its output without colors.
func Run(ctx context.Context, s Server, command string) (string, error) {
	if !nameRE.MatchString(s.Name) || !allowed[command] {
		return "", fmt.Errorf("%w: %s %s", ErrNotAllowed, s.Name, command)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(s.Dir, s.Name), command)
	cmd.Dir = s.Dir
	cmd.Env = []string{"HOME=" + s.Dir, "TERM=xterm", "PATH=/usr/local/bin:/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	return Clean(string(out)), err
}

func ParseStatus(details string) (string, error) {
	m := statusRE.FindStringSubmatch(Clean(details))
	if m == nil {
		return "", errors.New("no Status line in details output")
	}
	return m[1], nil
}

type Update struct {
	Available             bool
	Local, Remote, Branch string
}

// ParseCheckUpdate understands the "No update available" form seen on
// LinuxGSM v26.4.0. The positive form was not observed (nothing to update on
// the test box), so anything else that mentions "update available" counts as
// true and the rest is an error rather than a silent "false".
func ParseCheckUpdate(out string) (Update, error) {
	out = Clean(out)
	var u Update
	for _, m := range fieldRE.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "Local build":
			u.Local = strings.TrimSpace(m[2])
		case "Remote build":
			u.Remote = strings.TrimSpace(m[2])
		case "Branch":
			u.Branch = strings.TrimSpace(m[2])
		}
	}
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "no update available"):
	case strings.Contains(low, "update available"):
		u.Available = true
	default:
		return u, errors.New("unrecognised check-update output")
	}
	return u, nil
}
