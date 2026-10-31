package helper

import "testing"

func TestDepsForCombinesSharedAndGameRows(t *testing.T) {
	pkgs, ok := DepsFor("ubuntu-24.04", "mc")
	if !ok {
		t.Fatal("no list for ubuntu-24.04")
	}
	has := map[string]bool{}
	for _, p := range pkgs {
		has[p] = true
		if !PackageAllowed(p) {
			t.Errorf("%s from DepsFor would be refused by the helper", p)
		}
	}
	for _, want := range []string{"tmux", "curl", "openjdk-25-jre"} {
		if !has[want] {
			t.Errorf("missing %s in %v", want, pkgs)
		}
	}
	if has["rust"] || has["mc"] {
		t.Error("row names must not leak into the package list")
	}
	if has["steamcmd"] {
		t.Error("the steamcmd package is not part of a plain game's list")
	}
	if _, ok := DepsFor("slackware-15", "mc"); ok {
		t.Error("an unknown distro must report no list")
	}
	if _, ok := DepsFor("../etc/passwd", "mc"); ok {
		t.Error("a path in the distro name must not open anything")
	}
	dup := map[string]bool{}
	for _, p := range pkgs {
		if dup[p] {
			t.Errorf("duplicate package %s", p)
		}
		dup[p] = true
	}
}
