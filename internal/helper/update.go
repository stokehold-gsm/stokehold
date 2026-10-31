package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/selfupdate"
)

// The update runs in a transient systemd unit, outside the cgroup of the
// helper: the installer restarts the helper itself and must survive that.
const (
	updateUnit    = "stokehold-selfupdate"
	updateStaging = "/var/lib/stokehold-update"
)

// Test seams.
var (
	updateStage  = selfupdate.Stage
	updateRunner = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	updateArch = runtime.GOARCH
)

// StartUpdate stages the release (signature and hashes verified by the helper,
// as root, before anything runs) and starts the verified installer detached.
func StartUpdate(tag string) error {
	if !selfupdate.ValidTag(tag) {
		return fmt.Errorf("odd version %q", tag)
	}
	if out, _ := updateRunner("systemctl", "is-active", updateUnit); strings.TrimSpace(string(out)) == "active" {
		return errors.New("an update is already running")
	}
	updateRunner("systemctl", "reset-failed", updateUnit)
	dir := filepath.Join(updateStaging, tag)
	os.RemoveAll(dir)
	if err := os.MkdirAll(updateStaging, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := updateStage(ctx, nil, selfupdate.ReleaseURL, tag, updateArch, dir); err != nil {
		os.RemoveAll(dir)
		return err
	}
	out, err := updateRunner("systemd-run", "--unit="+updateUnit, "--collect", "--no-block",
		"/bin/bash", filepath.Join(dir, "install.sh"), "--local-dir", dir, "--version", tag)
	if err != nil {
		return fmt.Errorf("could not start the installer: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// UpdateStatus describes the last update run: whether the unit is still
// active and the tail of its output.
func UpdateStatus() (active bool, tail string) {
	out, _ := updateRunner("systemctl", "is-active", updateUnit)
	active = strings.TrimSpace(string(out)) == "active"
	log, _ := updateRunner("journalctl", "-u", updateUnit, "-n", "40", "--no-pager", "-o", "cat")
	s := strings.ToValidUTF8(string(log), "")
	if len(s) > 4000 {
		s = s[len(s)-4000:]
	}
	return active, s
}
