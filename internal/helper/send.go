package helper

// send types one line into the game console through LinuxGSM's `send`
// command. The text comes from a web form, so it never meets a shell: it is
// one argv element, and it may hold only printable characters on one line.

import (
	"os/user"
	"path"
	"unicode"
	"unicode/utf8"
)

const maxSendLen = 200

// ValidSendText accepts 1-200 printable characters, nothing that ends a line
// or steers a terminal.
func ValidSendText(s string) bool {
	if s == "" || len(s) > maxSendLen*4 || !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxSendLen {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) && r != ' ' {
			return false
		}
	}
	return true
}

// homeOf is a var so tests do not depend on the accounts of the machine.
var homeOf = func(account string) string {
	if u, err := user.Lookup(account); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return "/home/" + account
}

// sendArgv is the exact command line: no `sh -c`, the text is the last argument.
func sendArgv(account, game, text string) []string {
	home := homeOf(account)
	return []string{"runuser", "-u", account, "--", "env", "HOME=" + home, path.Join(home, game), "send", text}
}
