package web

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/selfupdate"
)

func updRig(t *testing.T, current, latest string) (*rig, *fakeHelper) {
	t.Helper()
	r := newRig(t)
	r.setup()
	r.login("")
	r.s.Version = current
	r.s.Releases = func(ctx context.Context, channel string) (*selfupdate.Release, error) {
		return &selfupdate.Release{Tag: latest, Notes: "what is new"}, nil
	}
	f := &fakeHelper{ok: true, out: "idle\n"}
	r.withHelper(f)
	return r, f
}

func TestUpdatesPageShowsANewerReleaseAndAppliesItThroughTheHelper(t *testing.T) {
	r, f := updRig(t, "v0.1.2", "v0.1.4")
	tok := r.csrf()
	if rec := r.do("POST", "/updates/check", url.Values{"csrf": {tok}}, nil); rec.Code != 303 {
		t.Fatalf("check: %d", rec.Code)
	}
	page := r.do("GET", "/updates", nil, nil).Body.String()
	if !strings.Contains(page, "v0.1.4") || !strings.Contains(page, "what is new") || !strings.Contains(page, `action="/updates/apply"`) {
		t.Fatalf("the page must offer the update:\n%s", page)
	}
	if got := r.s.UpdateAvailable(); got != "v0.1.4" {
		t.Errorf("UpdateAvailable = %q", got)
	}
	if rec := r.do("POST", "/updates/apply", url.Values{"csrf": {tok}, "version": {"v0.1.4"}}, nil); rec.Code != 303 {
		t.Fatalf("apply: %d", rec.Code)
	}
	last := f.calls[len(f.calls)-1]
	if last.Op != "update" || last.Version != "v0.1.4" {
		t.Fatalf("the helper must be asked to update to v0.1.4: %+v", last)
	}
}

func TestUpdateApplyRefusesOddOrOlderVersionsAndNonOwners(t *testing.T) {
	r, f := updRig(t, "v0.1.2", "v0.1.4")
	tok := r.csrf()
	for _, v := range []string{"latest", "v0.1.2", "v0.0.9", "v9.9.9; reboot"} {
		if rec := r.do("POST", "/updates/apply", url.Values{"csrf": {tok}, "version": {v}}, nil); rec.Code != 400 {
			t.Errorf("%q: %d", v, rec.Code)
		}
	}
	for _, c := range f.calls {
		if c.Op == "update" {
			t.Fatalf("no update may start for a refused version: %+v", c)
		}
	}
	code, pw := r.createUser(tok, "viewer@example.com", "viewer")
	if code != 200 && code != 303 {
		t.Fatalf("create viewer: %d", code)
	}
	r.loginAs("viewer@example.com", pw)
	if rec := r.do("GET", "/updates", nil, nil); rec.Code != 403 {
		t.Errorf("a viewer must not see the updates page: %d", rec.Code)
	}
}

func TestAutoUpdateWaitsForItsHourAndForInstalls(t *testing.T) {
	r, f := updRig(t, "v0.1.2", "v0.1.4")
	r.st.SetSetting(setUpdMode, "auto")
	r.st.SetSetting(setUpdHour, "4")
	count := func() (n int) {
		for _, c := range f.calls {
			if c.Op == "update" {
				n++
			}
		}
		return
	}
	r.s.Now = func() time.Time { return time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC) }
	r.s.updateTick(context.Background())
	if count() != 0 {
		t.Fatal("outside the chosen hour nothing may start")
	}
	if r.s.UpdateAvailable() != "v0.1.4" {
		t.Fatal("the check must still remember the new release")
	}
	r.s.Now = func() time.Time { return time.Date(2026, 11, 2, 4, 30, 0, 0, time.UTC) }
	r.s.jobs.running = true
	r.s.updateTick(context.Background())
	if count() != 0 {
		t.Fatal("a game server install must not be interrupted")
	}
	r.s.jobs.running = false
	r.s.updateTick(context.Background())
	if count() != 1 {
		t.Fatalf("auto mode must start the update in its hour: %d", count())
	}
}

func TestNotifyModeNeverInstalls(t *testing.T) {
	r, f := updRig(t, "v0.1.2", "v0.1.4")
	r.st.SetSetting(setUpdMode, "notify")
	r.s.Now = func() time.Time { return time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC) }
	r.s.updateTick(context.Background())
	for _, c := range f.calls {
		if c.Op == "update" {
			t.Fatal("notify mode must only report")
		}
	}
}

func TestNewVersionIsAnnouncedByDefaultAndNeverInstalledWithoutAsking(t *testing.T) {
	r, f := updRig(t, "v0.1.2", "v0.1.4")
	r.s.Now = func() time.Time { return time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC) }
	r.s.updateTick(context.Background()) // no mode saved: the default must be "notify"
	if r.s.UpdateAvailable() != "v0.1.4" {
		t.Fatal("by default the panel must look for a new version")
	}
	for _, c := range f.calls {
		if c.Op == "update" {
			t.Fatal("the default mode must never install by itself")
		}
	}
	page := r.do("GET", "/", nil, nil).Body.String()
	if !strings.Contains(page, "v0.1.4") || !strings.Contains(page, `action="/updates/apply"`) {
		t.Fatalf("the owner's front page must carry the update banner with a button:\n%s", page)
	}
	r.st.SetSetting(setUpdMode, "off")
	r.s.upd = updState{}
	r.s.updateTick(context.Background())
	if r.s.UpdateAvailable() != "" {
		t.Fatal("\"off\" must stop every request")
	}
}
