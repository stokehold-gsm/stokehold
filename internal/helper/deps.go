package helper

import (
	"embed"
	"encoding/csv"
	"strings"
)

// The helper installs packages as root, so the set is limited to what
// LinuxGSM itself lists for supported distros. Snapshots of its
// lgsm/data/<distro>.csv (MIT) are embedded; refresh them with each release
// (see docs/licenses.md). Column 0 is the game shortname and is not a package.
//
//go:embed deps/*.csv
var depsFS embed.FS

var allowedPkgs = loadAllowed()

func loadAllowed() map[string]bool {
	set := map[string]bool{}
	files, _ := depsFS.ReadDir("deps")
	for _, f := range files {
		r, err := depsFS.Open("deps/" + f.Name())
		if err != nil {
			continue
		}
		cr := csv.NewReader(r)
		cr.FieldsPerRecord = -1 // rows list a different number of packages
		rows, err := cr.ReadAll()
		r.Close()
		if err != nil {
			panic("bad embedded deps csv " + f.Name() + ": " + err.Error()) // would silently empty the allowlist
		}
		for _, row := range rows {
			for _, p := range row[1:] {
				if p = strings.TrimSpace(p); p != "" {
					set[p] = true
				}
			}
		}
	}
	return set
}

func PackageAllowed(p string) bool { return allowedPkgs[p] }
