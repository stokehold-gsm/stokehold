package helper

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidSendText(t *testing.T) {
	for _, s := range []string{"say hello", "/kick Bob cheating", "время рестарта 5 мин", "x"} {
		if !ValidSendText(s) {
			t.Errorf("%q must pass", s)
		}
	}
	for _, s := range []string{"", "a\nb", "a\rb", "bell\a", "esc\x1b[2J", "tab\there", strings.Repeat("a", 201), "\xff\xfe"} {
		if ValidSendText(s) {
			t.Errorf("%q must be refused", s)
		}
	}
}

func TestSendRunsWithoutAShellAndKeepsTheTextOneArgument(t *testing.T) {
	old := homeOf
	homeOf = func(a string) string { return "/home/" + a }
	defer func() { homeOf = old }()
	var got []string
	run := func(name string, args ...string) (string, error) {
		got = append([]string{name}, args...)
		return "sent", nil
	}
	text := `say "hi"; rm -rf / $(id)`
	r := HandleFull(Request{Op: "send", User: "gs_scp", Game: "scpslserver", Content: text}, Backend{Run: run})
	if !r.OK {
		t.Fatalf("%+v", r)
	}
	want := []string{"runuser", "-u", "gs_scp", "--", "env", "HOME=/home/gs_scp", "/home/gs_scp/scpslserver", "send", text}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	for _, a := range got {
		if a == "-c" || a == "sh" || a == "bash" {
			t.Fatalf("no shell may appear in %q", got)
		}
	}
	if r := HandleFull(Request{Op: "send", User: "gs_scp", Game: "scpslserver", Content: "a\nb"}, Backend{Run: run}); r.OK {
		t.Fatal("a newline must be refused")
	}
	if r := HandleFull(Request{Op: "send", User: "root", Game: "scpslserver", Content: "x"}, Backend{Run: run}); r.OK {
		t.Fatal("root is not a game user")
	}
}
