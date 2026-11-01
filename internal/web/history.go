package web

// Per-server history for the statistics tab and the sparklines on the list:
// one sample per status refresh (a minute), the last 24 hours, in memory only.
// A panel restart starts it over; that is fine for "how was it today".

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/lgsm"
)

const histMax = 1440

// Sample is one look at a server. CPU and Mem are -1 when LinuxGSM did not print them.
type Sample struct {
	T            time.Time
	State        string // started | starting | stopped | unknown
	Players, Max int
	HasPlayers   bool
	CPU          float64 // percent of one machine
	Mem          float64 // megabytes
}

type histStore struct {
	mu sync.Mutex
	m  map[int64][]Sample
}

// AddHistory records a sample; the demo uses it to fill a day at once.
func (s *Server) AddHistory(id int64, smp Sample) {
	s.hist.mu.Lock()
	defer s.hist.mu.Unlock()
	if s.hist.m == nil {
		s.hist.m = map[int64][]Sample{}
	}
	h := append(s.hist.m[id], smp)
	if len(h) > histMax {
		h = h[len(h)-histMax:]
	}
	s.hist.m[id] = h
}

func (s *Server) history(id int64) []Sample {
	s.hist.mu.Lock()
	defer s.hist.mu.Unlock()
	return append([]Sample(nil), s.hist.m[id]...)
}

var (
	pctRE = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*%`)
	memRE = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*([KMG])i?B`)
)

// usage reads "CPU Used: 4.2%" and "Mem Used: 18.0%   720MB" from details.
func usage(d lgsm.Details) (cpu, mem float64) {
	cpu, mem = -1, -1
	for _, sec := range d.Sections {
		for _, r := range sec.Rows {
			switch strings.ToLower(r.K) {
			case "cpu used":
				if m := pctRE.FindStringSubmatch(r.V); m != nil {
					cpu, _ = strconv.ParseFloat(m[1], 64)
				}
			case "mem used":
				if m := memRE.FindStringSubmatch(r.V); m != nil {
					v, _ := strconv.ParseFloat(m[1], 64)
					switch strings.ToUpper(m[2]) {
					case "K":
						v /= 1024
					case "G":
						v *= 1024
					}
					mem = v
				}
			}
		}
	}
	return cpu, mem
}

// since is the start of the current state, looking back through the history.
func since(h []Sample) time.Time {
	if len(h) == 0 {
		return time.Time{}
	}
	cur := h[len(h)-1].State
	t := h[len(h)-1].T
	for i := len(h) - 1; i >= 0 && h[i].State == cur; i-- {
		t = h[i].T
	}
	return t
}

func ago(lang string, d time.Duration) string {
	switch {
	case d < time.Minute:
		return tr(lang, "ago_now")
	case d < time.Hour:
		return fmt.Sprintf(tr(lang, "ago_min"), int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf(tr(lang, "ago_h"), int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf(tr(lang, "ago_d"), int(d.Hours()/24))
}

// chart is a ready-to-draw SVG series: a polyline and the area under it,
// in a viewBox of W x H.
type chart struct {
	Line, Area string
	Max, Last  string
	Has        bool
}

const chartW, chartH = 600.0, 120.0

// series turns values (negative = missing) into chart points; max 0 means
// scale to the largest value.
func series(vals []float64, max float64) chart {
	var pts []string
	top := max
	if top <= 0 {
		for _, v := range vals {
			if v > top {
				top = v
			}
		}
	}
	if top <= 0 {
		top = 1
	}
	n := len(vals)
	last := -1.0
	for i, v := range vals {
		if v < 0 {
			continue
		}
		x := chartW
		if n > 1 {
			x = chartW * float64(i) / float64(n-1)
		}
		y := chartH - chartH*min(v, top)/top
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
		last = v
	}
	if len(pts) < 2 {
		return chart{}
	}
	line := strings.Join(pts, " ")
	first := strings.Split(pts[0], ",")[0]
	end := strings.Split(pts[len(pts)-1], ",")[0]
	return chart{Line: line, Area: "M" + first + "," + fmt.Sprint(chartH) + " L" + strings.ReplaceAll(line, " ", " L") + " L" + end + "," + fmt.Sprint(chartH) + " Z",
		Max: strconv.FormatFloat(top, 'f', -1, 64), Last: strconv.FormatFloat(last, 'f', 1, 64), Has: true}
}

// band is one stretch of the uptime bar.
type band struct {
	X, W  string
	State string
}

func timeline(h []Sample) []band {
	if len(h) == 0 {
		return nil
	}
	var out []band
	start := 0
	n := float64(len(h))
	flush := func(i int) {
		out = append(out, band{X: fmt.Sprintf("%.2f", 100*float64(start)/n), W: fmt.Sprintf("%.2f", 100*float64(i-start)/n), State: h[start].State})
		start = i
	}
	for i := 1; i < len(h); i++ {
		if h[i].State != h[start].State {
			flush(i)
		}
	}
	flush(len(h))
	return out
}
