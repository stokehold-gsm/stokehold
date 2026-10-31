package helper

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func only(users ...string) Adopted {
	return func(u string) bool {
		for _, x := range users {
			if x == u {
				return true
			}
		}
		return false
	}
}

func TestAdoptedAccountsMayOnlyBeRunAndInspected(t *testing.T) {
	ad := only("factorio", "root")
	ok := []Request{
		{Op: "run", User: "factorio", Game: "fctrserver", Cmd: "details"},
		{Op: "discover", User: "factorio"},
	}
	for _, r := range ok {
		if err := ValidateWith(r, ad); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Request{
		{Op: "create-user", User: "factorio"},                              // would modify a real account
		{Op: "install", User: "factorio", Game: "fctrserver"},              // would install into it
		{Op: "run", User: "root", Game: "mcserver", Cmd: "start"},          // listed, but never root
		{Op: "run", User: "someone", Game: "mcserver", Cmd: "start"},       // not listed
		{Op: "run", User: "Factorio", Game: "fctrserver", Cmd: "start"},    // wrong shape
		{Op: "run", User: "fact orio", Game: "fctrserver", Cmd: "start"},   // wrong shape
		{Op: "run", User: "factorio;id", Game: "fctrserver", Cmd: "start"}, // injection attempt
		{Op: "run", User: "factorio", Game: "fctrserver", Cmd: "install"},  // command not whitelisted
		{Op: "discover", User: "someone"},                                  // not listed
		{Op: "discover", User: "-c"},                                       // would look like an option
	}
	for _, r := range bad {
		if err := ValidateWith(r, ad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", r, err)
		}
	}
	// without an adopt list nothing outside gs_ is reachable
	if err := Validate(Request{Op: "run", User: "factorio", Game: "fctrserver", Cmd: "start"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no adopt list: %v", err)
	}
}

func TestDiscoverFiltersTheListing(t *testing.T) {
	run := func(name string, args ...string) (string, error) {
		if name != "runuser" || args[len(args)-1] != "ls -1 lgsm/config-lgsm" {
			t.Errorf("unexpected command %s %v", name, args)
		}
		return "common.cfg\nfctrserver\n../etc\nbash\nmcserver\n; rm -rf /\n", nil
	}
	resp := HandleWith(Request{Op: "discover", User: "factorio"}, run, only("factorio"))
	if !resp.OK || resp.Out != "fctrserver\nmcserver" {
		t.Errorf("%+v", resp)
	}
}

func TestAdoptFileIsReadFreshAndMatchesWholeLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adopt.conf")
	ad := AdoptedFromFile(path)
	if ad("factorio") {
		t.Error("a missing file must allow nothing")
	}
	os.WriteFile(path, []byte("# accounts the panel may manage\n\nfactorio\n  valheim  \n#rust\n"), 0o600)
	for user, want := range map[string]bool{"factorio": true, "valheim": true, "rust": false, "factor": false, "factorio2": false, "": false} {
		if got := ad(user); got != want {
			t.Errorf("%q: got %v, want %v", user, got, want)
		}
	}
	os.WriteFile(path, []byte("minecraft\n"), 0o600)
	if ad("factorio") || !ad("minecraft") {
		t.Error("edits to the file must take effect without a restart")
	}
}

func TestDiscoverRunsWithoutShellInterpolation(t *testing.T) {
	var got []string
	run := func(name string, args ...string) (string, error) {
		got = append([]string{name}, args...)
		return "", nil
	}
	HandleWith(Request{Op: "run", User: "factorio", Game: "fctrserver", Cmd: "details"}, run, only("factorio"))
	if strings.Join(got, " ") != "runuser -l factorio -c ./fctrserver details" {
		t.Errorf("command line: %q", got)
	}
}
