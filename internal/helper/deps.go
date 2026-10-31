package helper

import (
	"embed"
	"encoding/csv"
	"os"
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

// DepsFor returns what LinuxGSM lists for a game on a distro such as
// "ubuntu-22.04": the shared "all" row plus the game's own row. ok is false
// when there is no list for that distro.
func DepsFor(distro, short string) (pkgs []string, ok bool) {
	r, err := depsFS.Open("deps/" + distro + ".csv")
	if err != nil {
		return nil, false
	}
	defer r.Close()
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) == 0 || (row[0] != "all" && row[0] != short) {
			continue
		}
		for _, p := range row[1:] {
			if p = strings.TrimSpace(p); p != "" && !seen[p] {
				seen[p] = true
				pkgs = append(pkgs, p)
			}
		}
	}
	return pkgs, true
}

// CurrentDistro reads /etc/os-release and returns an id like "ubuntu-22.04",
// or "" when it cannot tell.
func CurrentDistro() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	var id, ver string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "ID":
			id = v
		case "VERSION_ID":
			ver = v
		}
	}
	if id == "" || ver == "" {
		return ""
	}
	return id + "-" + ver
}
