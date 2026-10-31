package helper

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func stubUpdate(t *testing.T, stageErr error) *[]string {
	t.Helper()
	var calls []string
	oldStage, oldRun := updateStage, updateRunner
	t.Cleanup(func() { updateStage, updateRunner = oldStage, oldRun })
	updateStage = func(ctx context.Context, c *http.Client, base, tag, arch, dir string) error { return stageErr }
	updateRunner = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("inactive"), nil
	}
	return &calls
}

func TestStartUpdateRejectsOddTagsBeforeAnything(t *testing.T) {
	calls := stubUpdate(t, nil)
	for _, tag := range []string{"", "latest", "v1.2.3; reboot", "../v1.2.3"} {
		if err := StartUpdate(tag); err == nil {
			t.Errorf("%q must be refused", tag)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing may run for a bad tag: %v", *calls)
	}
}

func TestStartUpdateStagesThenRunsTheVerifiedInstallerDetached(t *testing.T) {
	calls := stubUpdate(t, nil)
	if err := StartUpdate("v1.2.3"); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if !strings.HasPrefix(last, "systemd-run --unit=stokehold-selfupdate --collect --no-block /bin/bash ") || !strings.Contains(last, "--local-dir") || !strings.HasSuffix(last, "--version v1.2.3") {
		t.Fatalf("unexpected command: %s", last)
	}
}

func TestStartUpdateDoesNotRunAnythingWhenTheReleaseDoesNotVerify(t *testing.T) {
	calls := stubUpdate(t, errors.New("signature check failed"))
	if err := StartUpdate("v1.2.3"); err == nil {
		t.Fatal("a release that fails verification must not be installed")
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "systemd-run") {
			t.Fatalf("the installer was started for an unverified release: %s", c)
		}
	}
}

func TestUpdateOpsNeedTheMainHelper(t *testing.T) {
	calls := stubUpdate(t, nil)
	r := HandleFull(Request{Op: "update", Version: "v1.2.3"}, Backend{})
	if r.OK || !strings.Contains(r.Err, "not available") || len(*calls) != 0 {
		t.Fatalf("an agent must not update the panel: %+v %v", r, *calls)
	}
	if r := HandleFull(Request{Op: "update", Version: "v1.2.3"}, Backend{Update: true}); !r.OK {
		t.Fatalf("the main helper starts the update: %+v", r)
	}
	if r := HandleFull(Request{Op: "update", Version: "latest"}, Backend{Update: true}); r.OK {
		t.Fatal("an odd version must be refused")
	}
}
