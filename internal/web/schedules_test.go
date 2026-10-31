package web

import (
	"net/url"
	"strings"
	"testing"

	"stokehold/internal/store"
)

func schedPath(id int64) string { return "/servers/" + string(rune('0'+id)) + "/schedules" }

func addSched(r *rig, tok string, id int64, extra url.Values) int {
	form := url.Values{"csrf": {tok}}
	for k, v := range extra {
		form[k] = v
	}
	return r.do("POST", schedPath(id), form, nil).Code
}

func TestOwnerAddsAndDeletesSchedules(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	tok := r.csrf()
	if code := addSched(r, tok, id, url.Values{"cmd": {"backup"}, "kind": {"daily"}, "time": {"03:30"}}); code != 303 {
		t.Fatalf("daily: %d", code)
	}
	if code := addSched(r, tok, id, url.Values{"cmd": {"restart"}, "kind": {"interval"}, "hours": {"12"}}); code != 303 {
		t.Fatalf("interval: %d", code)
	}
	list, _ := r.st.SchedulesFor(id)
	if len(list) != 2 || list[0].Value != 3*60+30 || list[1].Value != 12 {
		t.Fatalf("stored: %+v", list)
	}
	page := r.do("GET", schedPath(id), nil, nil).Body.String()
	for _, want := range []string{"03:30 UTC", "Every 12 h", "not run yet", "stops the game server"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if r.st.AuditCount("schedule.add") != 2 {
		t.Error("additions not audited")
	}
	if rec := r.do("POST", "/schedules/1/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 303 {
		t.Errorf("delete: %d", rec.Code)
	}
	if list, _ := r.st.SchedulesFor(id); len(list) != 1 {
		t.Errorf("after delete: %d", len(list))
	}
	if rec := r.do("POST", "/schedules/99/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 404 {
		t.Errorf("unknown schedule: %d", rec.Code)
	}
}

func TestScheduleInputIsValidated(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	tok := r.csrf()
	for name, f := range map[string]url.Values{
		"stop is not schedulable":   {"cmd": {"stop"}, "kind": {"daily"}, "time": {"03:00"}},
		"console":                   {"cmd": {"console"}, "kind": {"daily"}, "time": {"03:00"}},
		"hour 24":                   {"cmd": {"backup"}, "kind": {"daily"}, "time": {"24:00"}},
		"minute 60":                 {"cmd": {"backup"}, "kind": {"daily"}, "time": {"03:60"}},
		"short form":                {"cmd": {"backup"}, "kind": {"daily"}, "time": {"3:5"}},
		"empty time":                {"cmd": {"backup"}, "kind": {"daily"}, "time": {""}},
		"zero hours":                {"cmd": {"backup"}, "kind": {"interval"}, "hours": {"0"}},
		"too many hours":            {"cmd": {"backup"}, "kind": {"interval"}, "hours": {"169"}},
		"hours not a number":        {"cmd": {"backup"}, "kind": {"interval"}, "hours": {"abc"}},
		"unknown kind":              {"cmd": {"backup"}, "kind": {"weekly"}, "time": {"03:00"}},
		"injection in the time box": {"cmd": {"backup"}, "kind": {"daily"}, "time": {"03:00;id"}},
	} {
		if code := addSched(r, tok, id, f); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if list, _ := r.st.SchedulesFor(id); len(list) != 0 {
		t.Errorf("invalid schedules were stored: %+v", list)
	}
	if code := addSched(r, "wrong", id, url.Values{"cmd": {"backup"}, "kind": {"daily"}, "time": {"03:00"}}); code != 403 {
		t.Errorf("bad csrf: %d", code)
	}
	if rec := r.do("POST", "/servers/99/schedules", url.Values{"csrf": {tok}, "cmd": {"backup"}, "kind": {"daily"}, "time": {"03:00"}}, nil); rec.Code != 404 {
		t.Errorf("unknown server: %d", rec.Code)
	}
}

func TestViewersSeeSchedulesButCannotChangeThem(t *testing.T) {
	r := newRig(t)
	r.setup()
	id := r.addServerRow()
	r.st.AddSchedule(id, "backup", store.KindDaily, 180, r.s.Now())
	r.st.CreateUser("viewer@example.com", pass, store.RoleViewer)
	r.loginAs("viewer@example.com", pass)
	tok := r.csrf()
	page := r.do("GET", schedPath(id), nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "03:00 UTC") {
		t.Fatalf("view: %d", page.Code)
	}
	if strings.Contains(page.Body.String(), `action="/schedules/1/delete"`) || strings.Contains(page.Body.String(), `name="cmd"`) {
		t.Error("a viewer must not see the edit forms")
	}
	if code := addSched(r, tok, id, url.Values{"cmd": {"backup"}, "kind": {"daily"}, "time": {"04:00"}}); code != 403 {
		t.Errorf("viewer add: %d", code)
	}
	if rec := r.do("POST", "/schedules/1/delete", url.Values{"csrf": {tok}}, nil); rec.Code != 403 {
		t.Errorf("viewer delete: %d", rec.Code)
	}
	if list, _ := r.st.SchedulesFor(id); len(list) != 1 {
		t.Error("a viewer changed the schedules")
	}
}

func TestDashboardLinksToSchedules(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.login("")
	id := r.addServerRow()
	if body := r.do("GET", "/", nil, nil).Body.String(); !strings.Contains(body, schedPath(id)) {
		t.Error("no link to the schedule page")
	}
}
