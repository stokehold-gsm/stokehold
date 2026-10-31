package helper

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRejects(t *testing.T) {
	bad := []Request{
		{Op: "create-user", User: "root"},
		{Op: "create-user", User: "factorio"},
		{Op: "create-user", User: "gs_"},
		{Op: "create-user", User: "gs_../etc"},
		{Op: "create-user", User: "gs_a b"},
		{Op: "create-user", User: "gs_x;id"},
		{Op: "create-user", User: "GS_x"},
		{Op: "install", User: "gs_a", Game: "mcserver; rm -rf /"},
		{Op: "install", User: "gs_a", Game: "bash"},
		{Op: "run", User: "gs_a", Game: "mcserver", Cmd: "console"},
		{Op: "run", User: "gs_a", Game: "mcserver", Cmd: "start; id"},
		{Op: "run", User: "gs_a", Game: "../mcserver", Cmd: "start"},
		{Op: "deps"},
		{Op: "deps", Packages: []string{"-o", "x"}},
		{Op: "deps", Packages: []string{"a b"}},
		{Op: "deps", Packages: []string{"x;id"}},
		{Op: "deps", Packages: []string{"openssh-server"}},
		{Op: "deps", Packages: []string{"docker.io"}},
		{Op: "deps", Packages: []string{"openjdk-25-jre", "netcat-traditional"}},
		{Op: "deps", Packages: make([]string, 21)},
		{Op: "reboot", User: "gs_a"},
		{},
	}
	for _, r := range bad {
		if err := Validate(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", r, err)
		}
	}
}

func TestHandleBuildsFixedCommands(t *testing.T) {
	var got []string
	run := func(name string, args ...string) (string, error) {
		got = append(got, name+" "+strings.Join(args, " "))
		return "", nil
	}
	cases := []struct {
		req  Request
		want []string
	}{
		{Request{Op: "create-user", User: "gs_a1"}, []string{
			"useradd --create-home --shell /bin/bash --user-group gs_a1",
			"chmod 700 /home/gs_a1"}},
		{Request{Op: "run", User: "gs_a1", Game: "mcserver", Cmd: "start"}, []string{
			"runuser -l gs_a1 -c ./mcserver start"}},
		{Request{Op: "deps", Packages: []string{"openjdk-25-jre", "lib32gcc-s1", "libsdl2-2.0-0:i386"}}, []string{
			"apt-get install -y --no-install-recommends -- openjdk-25-jre lib32gcc-s1 libsdl2-2.0-0:i386"}},
	}
	for _, c := range cases {
		got = nil
		if resp := Handle(c.req, run); !resp.OK {
			t.Fatalf("%+v: %+v", c.req, resp)
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%+v:\n got %q\nwant %q", c.req, got, c.want)
		}
	}
}

func TestHandleNeverRunsInvalid(t *testing.T) {
	called := false
	run := func(string, ...string) (string, error) { called = true; return "", nil }
	if resp := Handle(Request{Op: "create-user", User: "factorio"}, run); resp.OK || called {
		t.Errorf("invalid request executed: %+v called=%v", resp, called)
	}
}

func TestHandleStopsAfterFailedUseradd(t *testing.T) {
	n := 0
	run := func(string, ...string) (string, error) { n++; return "exists", errors.New("exit 9") }
	if resp := Handle(Request{Op: "create-user", User: "gs_a1"}, run); resp.OK || n != 1 {
		t.Errorf("must not chmod an existing home: %+v n=%d", resp, n)
	}
}

func TestPackageAllowlist(t *testing.T) {
	for _, p := range []string{"openjdk-25-jre", "lib32gcc-s1", "libsdl2-2.0-0:i386", "tmux", "steamcmd"} {
		if !PackageAllowed(p) {
			t.Errorf("%s should come from the LinuxGSM lists", p)
		}
	}
	for _, p := range []string{"openssh-server", "docker.io", "netcat-traditional", "", "all"} {
		if PackageAllowed(p) {
			t.Errorf("%s must not be installable", p)
		}
	}
}
