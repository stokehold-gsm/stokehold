package web

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/lgsm"
)

func TestUsageReadsCPUAndMemoryFromDetails(t *testing.T) {
	d := lgsm.ParseDetails("=====\nServer Resource\n=====\nCPU Used:  4.2%\nMem Used:  18.0%   1.5GB\n")
	cpu, mem := usage(d)
	if cpu != 4.2 || mem != 1536 {
		t.Fatalf("cpu %v mem %v", cpu, mem)
	}
	if c, m := usage(lgsm.Details{}); c != -1 || m != -1 {
		t.Fatal("unknown values must be -1")
	}
}

func TestSeriesSkipsGapsAndScales(t *testing.T) {
	c := series([]float64{-1, 0, 5, 10}, 10)
	if !c.Has || strings.Count(c.Line, ",") != 3 || !strings.HasSuffix(c.Line, "600.0,0.0") || c.Last != "10.0" {
		t.Fatalf("%+v", c)
	}
	if series([]float64{-1, 3}, 0).Has {
		t.Fatal("one point is not a line")
	}
}

func TestTimelineMergesRunsOfTheSameState(t *testing.T) {
	h := []Sample{{State: "started"}, {State: "started"}, {State: "stopped"}, {State: "started"}}
	b := timeline(h)
	if len(b) != 3 || b[0].W != "50.00" || b[1].State != "stopped" || b[2].X != "75.00" {
		t.Fatalf("%+v", b)
	}
}

func histRig(t *testing.T) (*rig, int64, *[]helper.Request) {
	t.Helper()
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	var calls []helper.Request
	r.s.Helper = func(req helper.Request) (helper.Response, error) {
		calls = append(calls, req)
		if req.Op == "run" {
			return helper.Response{OK: true, Out: "=====\nServer Resource\n=====\nCPU Used:  12%\nMem Used:  5%  640MB\n\n=====\nGame Details\n=====\nPlayers:  4/10\nStatus:  STARTED\n"}, nil
		}
		return helper.Response{OK: true, Out: "ok"}, nil
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 90; i++ {
		st := "started"
		if i < 30 {
			st = "stopped"
		}
		r.s.AddHistory(id, Sample{T: now.Add(time.Duration(i-90) * time.Minute), State: st, Players: i % 10, Max: 10, HasPlayers: true, CPU: 10, Mem: 600})
	}
	r.s.Now = func() time.Time { return now }
	old := sendSettle
	sendSettle = 0
	t.Cleanup(func() { sendSettle = old })
	return r, id, &calls
}

func TestServersListShowsLiveFactsFromHistory(t *testing.T) {
	r, _, _ := histRig(t)
	page := r.do("GET", "/", nil, nil).Body.String()
	for _, want := range []string{`class="spark players"`, "<progress", "600 MB", "10%", `?tab=console"`, `?tab=stats"`, "1 h 0 min"} {
		if !strings.Contains(page, want) {
			t.Errorf("the list lacks %q", want)
		}
	}
}

func TestStatsTabDrawsCharts(t *testing.T) {
	r, id, _ := histRig(t)
	page := r.do("GET", "/servers/"+strconv.FormatInt(id, 10)+"?tab=stats", nil, nil).Body.String()
	for _, want := range []string{`class="chart c-players"`, `class="chart c-cpu"`, `class="chart c-mem"`, `class="tl-stopped"`, `class="tl-started"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the stats tab lacks %q", want)
		}
	}
}

func TestConsoleSendGoesToTheHelperForControllersOnly(t *testing.T) {
	r, id, calls := histRig(t)
	path := "/servers/" + strconv.FormatInt(id, 10) + "/send"
	tok := r.csrf()
	rec := r.do("POST", path, url.Values{"csrf": {tok}, "text": {"say hi"}}, nil)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "sent=ok") {
		t.Fatalf("send: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	last := (*calls)[len(*calls)-1]
	if last.Op != "send" || last.Content != "say hi" || last.Game != "fctrserver" || last.User != "factorio" {
		t.Fatalf("helper got %+v", last)
	}
	n := len(*calls)
	if rec := r.do("POST", path, url.Values{"csrf": {tok}, "text": {"two\nlines"}}, nil); !strings.Contains(rec.Header().Get("Location"), "sent=bad") || len(*calls) != n {
		t.Fatal("a multi-line command must be refused before the helper")
	}
	code, pw := r.createUser(tok, "v@example.com", "viewer")
	if code != 200 && code != 303 {
		t.Fatalf("viewer: %d", code)
	}
	r.loginAs("v@example.com", pw)
	if rec := r.do("POST", path, url.Values{"csrf": {r.csrf()}, "text": {"say hi"}}, nil); rec.Code != 403 {
		t.Fatalf("a viewer must not type into the console: %d", rec.Code)
	}
}

func TestConsoleSendIsRateLimited(t *testing.T) {
	r, id, _ := histRig(t)
	path := "/servers/" + strconv.FormatInt(id, 10) + "/send"
	tok := r.csrf()
	var last string
	for i := 0; i < 31; i++ {
		last = r.do("POST", path, url.Values{"csrf": {tok}, "text": {"say " + strconv.Itoa(i)}}, nil).Header().Get("Location")
	}
	if !strings.Contains(last, "sent=slow") {
		t.Fatalf("the 31st line in a minute must be refused: %s", last)
	}
}
