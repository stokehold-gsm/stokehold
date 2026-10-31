package helper

import (
	"bufio"
	"os"
	"strings"

	"stokehold/internal/lgsm"
)

// onlyServerNames keeps the lines of a directory listing that look like
// LinuxGSM script names, so the panel never sees anything else.
func onlyServerNames(listing string) string {
	var names []string
	for _, l := range strings.Split(listing, "\n") {
		if l = strings.TrimSpace(l); lgsm.ValidName(l) {
			names = append(names, l)
		}
	}
	return strings.Join(names, "\n")
}

// AdoptedFromFile reads the list of existing Linux accounts the owner allowed
// the panel to manage, one per line, '#' comments. The file is read on every
// check so editing it takes effect at once, and it must be root-owned: the
// panel user can write neither it nor its directory.
func AdoptedFromFile(path string) Adopted {
	return func(user string) bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" && !strings.HasPrefix(line, "#") && line == user {
				return true
			}
		}
		return false
	}
}
