package lgsm

import (
	"regexp"
	"strconv"
	"strings"
)

// Details is LinuxGSM's `details` output turned into something a page can lay
// out. The parser is tolerant on purpose: a game that prints extra sections or
// rows still gets them shown, and anything unrecognised is simply left out.
type Details struct {
	Sections   []Section
	Ports      []Port
	Status     string // STARTED, STOPPED, ... as printed
	Players    int
	Max        int
	HasPlayers bool
}

type Section struct {
	Title string
	Rows  []Row
}

type Row struct {
	K, V   string
	Secret bool // a password, token or similar: the page masks it
}

type Port struct {
	Name, Port, Proto string
	Listening         bool
}

var (
	ruleRE   = regexp.MustCompile(`^[=\-─]{5,}\s*$`)
	rowRE    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 ./()_-]{0,38}?):\s{1,}(\S.*)$`)
	portRE   = regexp.MustCompile(`^>?\s*(\S.*?)\s{2,}(\d{2,5})\s+(\w+)\s+(YES|NO)\b`)
	secretRE = regexp.MustCompile(`(?i)pass(word|phrase)?|rcon|token|secret|key$`)
)

// ParseDetails reads cleaned or raw `details` output.
func ParseDetails(raw string) Details {
	lines := strings.Split(strings.ReplaceAll(Clean(raw), "\r", ""), "\n")
	var d Details
	var cur *Section
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if ruleRE.MatchString(line) {
			if i+2 < len(lines) && ruleRE.MatchString(strings.TrimSpace(lines[i+2])) && strings.TrimSpace(lines[i+1]) != "" {
				d.Sections = append(d.Sections, Section{Title: strings.TrimSpace(lines[i+1])})
				cur = &d.Sections[len(d.Sections)-1]
				i += 2
			}
			continue
		}
		if m := portRE.FindStringSubmatch(line); m != nil {
			d.Ports = append(d.Ports, Port{Name: strings.TrimSpace(m[1]), Port: m[2], Proto: strings.ToLower(m[3]), Listening: m[4] == "YES"})
			continue
		}
		m := rowRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || cur == nil {
			continue
		}
		k, v := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if strings.EqualFold(k, "Status") && d.Status == "" {
			d.Status = strings.ToUpper(strings.Fields(v)[0])
		}
		if strings.EqualFold(k, "Players") || strings.EqualFold(k, "Current players") {
			if a, b, ok := splitPlayers(v); ok {
				d.Players, d.Max, d.HasPlayers = a, b, true
			}
		}
		cur.Rows = append(cur.Rows, Row{K: k, V: v, Secret: secretRE.MatchString(k) && !strings.EqualFold(v, "NOT SET")})
	}
	if !d.HasPlayers {
		if a, b, ok := ParsePlayers(raw); ok {
			d.Players, d.Max, d.HasPlayers = a, b, true
		}
	}
	// drop sections that held nothing we could read
	keep := d.Sections[:0]
	for _, s := range d.Sections {
		if len(s.Rows) > 0 {
			keep = append(keep, s)
		}
	}
	d.Sections = keep
	return d
}

func splitPlayers(v string) (a, b int, ok bool) {
	x, y, found := strings.Cut(strings.Fields(v)[0], "/")
	if !found {
		return 0, 0, false
	}
	na, e1 := strconv.Atoi(x)
	nb, e2 := strconv.Atoi(y)
	return na, nb, e1 == nil && e2 == nil
}

// State is what the panel shows: LinuxGSM only prints STARTED or STOPPED, but a
// server that is started while none of its game ports listens yet is still
// booting.
func (d Details) State() string {
	switch d.Status {
	case "STARTED":
		if len(d.Ports) > 0 {
			any := false
			for _, p := range d.Ports {
				any = any || p.Listening
			}
			if !any {
				return "STARTING"
			}
		}
		return "STARTED"
	case "":
		return ""
	}
	return d.Status
}
