package web

// Panel updates: the owner sees the newest release, starts an update with one
// button or lets the panel do it in a chosen hour. The root helper verifies the
// signed release and runs the installer (which rolls back by itself); this file
// only decides when and shows the result.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/selfupdate"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

const (
	setUpdMode    = "update_mode"    // off | notify | auto
	setUpdChannel = "update_channel" // stable | beta
	setUpdHour    = "update_hour"    // 0-23, UTC, for auto
)

type updState struct {
	mu      sync.Mutex
	latest  *selfupdate.Release
	checked time.Time
	err     string
}

type updView struct {
	Current   string
	Latest    *selfupdate.Release
	Newer     bool
	Checked   string
	CheckErr  string
	Mode      string
	Channel   string
	Hour      int
	Active    bool
	Log       string
	Dev       bool
	Started   bool
	LastError string
}

func (s *Server) updSetting(key, def string) string {
	if v, ok := s.Store.Setting(key); ok && v != "" {
		return v
	}
	return def
}

func (s *Server) updHour() int {
	h, err := strconv.Atoi(s.updSetting(setUpdHour, "4"))
	if err != nil || h < 0 || h > 23 {
		return 4
	}
	return h
}

func (s *Server) releases(ctx context.Context, channel string) (*selfupdate.Release, error) {
	if s.Releases != nil {
		return s.Releases(ctx, channel)
	}
	return selfupdate.Latest(ctx, nil, selfupdate.APIBase, channel)
}

// CheckUpdate asks for the newest release of the chosen channel and remembers the answer.
func (s *Server) CheckUpdate(ctx context.Context) {
	rel, err := s.releases(ctx, s.updSetting(setUpdChannel, "stable"))
	s.upd.mu.Lock()
	defer s.upd.mu.Unlock()
	s.upd.checked = s.Now()
	if err != nil {
		s.upd.err = err.Error()
		return
	}
	s.upd.latest, s.upd.err = rel, ""
}

// UpdateAvailable is the tag of a newer release found by the last check, or "".
func (s *Server) UpdateAvailable() string {
	s.upd.mu.Lock()
	defer s.upd.mu.Unlock()
	if s.upd.latest != nil && selfupdate.Newer(s.Version, s.upd.latest.Tag) {
		return s.upd.latest.Tag
	}
	return ""
}

func (s *Server) updView() *updView {
	v := &updView{Current: s.Version, Dev: !selfupdate.ValidTag(s.Version),
		Mode: s.updSetting(setUpdMode, "off"), Channel: s.updSetting(setUpdChannel, "stable"), Hour: s.updHour()}
	s.upd.mu.Lock()
	v.Latest, v.CheckErr = s.upd.latest, s.upd.err
	if !s.upd.checked.IsZero() {
		v.Checked = s.upd.checked.UTC().Format("2006-01-02 15:04 UTC")
	}
	s.upd.mu.Unlock()
	v.Newer = v.Latest != nil && selfupdate.Newer(s.Version, v.Latest.Tag)
	if resp, err := s.callHelper(helper.Request{Op: "update-status"}); err == nil && resp.OK {
		state, log, _ := strings.Cut(resp.Out, "\n")
		v.Active, v.Log = state == "active", strings.TrimSpace(log)
	}
	return v
}

func (s *Server) updRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /updates", s.auth(s.updatesPage))
	mux.HandleFunc("POST /updates/settings", s.auth(s.updatesSave))
	mux.HandleFunc("POST /updates/check", s.auth(s.updatesCheck))
	mux.HandleFunc("POST /updates/apply", s.auth(s.updatesApply))
}

func (s *Server) updatesPage(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	p.Upd = s.updView()
	p.Upd.Started = r.URL.Query().Get("started") == "1"
	p.Refresh = p.Upd.Active || p.Upd.Started
	s.render(w, "updates", p, http.StatusOK)
}

func (s *Server) updatesSave(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	mode, channel := r.PostFormValue("mode"), r.PostFormValue("channel")
	hour, err := strconv.Atoi(r.PostFormValue("hour"))
	if (mode != "off" && mode != "notify" && mode != "auto") || (channel != "stable" && channel != "beta") || err != nil || hour < 0 || hour > 23 {
		p.Error = tr(p.Lang, "upd_bad")
		p.Upd = s.updView()
		s.render(w, "updates", p, http.StatusBadRequest)
		return
	}
	s.Store.SetSetting(setUpdMode, mode)
	s.Store.SetSetting(setUpdChannel, channel)
	s.Store.SetSetting(setUpdHour, strconv.Itoa(hour))
	s.Store.Audit(p.User.ID, "update.settings", mode+" "+channel+" "+strconv.Itoa(hour))
	if mode != "off" {
		go s.CheckUpdate(context.Background())
	}
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
}

func (s *Server) updatesCheck(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	s.CheckUpdate(ctx)
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
}

func (s *Server) updatesApply(w http.ResponseWriter, r *http.Request, p *page) {
	if p.User.Role != store.RoleOwner {
		s.denied(w, p)
		return
	}
	tag := r.PostFormValue("version")
	if !selfupdate.ValidTag(tag) || !selfupdate.Newer(s.Version, tag) {
		p.Error = tr(p.Lang, "upd_bad")
		p.Upd = s.updView()
		s.render(w, "updates", p, http.StatusBadRequest)
		return
	}
	if err := s.startUpdate(tag); err != nil {
		p.Error = err.Error()
		p.Upd = s.updView()
		s.render(w, "updates", p, http.StatusBadGateway)
		return
	}
	s.Store.Audit(p.User.ID, "update.apply", s.Version+" -> "+tag)
	http.Redirect(w, r, "/updates?started=1", http.StatusSeeOther)
}

func (s *Server) startUpdate(tag string) error {
	resp, err := s.callHelper(helper.Request{Op: "update", Version: tag})
	if err != nil {
		return err
	}
	if !resp.OK {
		return &updError{resp.Err}
	}
	return nil
}

// installBusy is true while a game server install runs: an update must not interrupt it.
func (s *Server) installBusy() bool {
	s.jobs.mu.Lock()
	defer s.jobs.mu.Unlock()
	return s.jobs.running
}

type updError struct{ msg string }

func (e *updError) Error() string { return e.msg }

// UpdateLoop checks hourly when the owner asked for it and, in auto mode,
// starts the update in the chosen hour (UTC) while no game server install runs.
func (s *Server) UpdateLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.updateTick(ctx)
		}
	}
}

func (s *Server) updateTick(ctx context.Context) {
	mode := s.updSetting(setUpdMode, "off")
	if mode == "off" {
		return
	}
	s.CheckUpdate(ctx)
	tag := s.UpdateAvailable()
	if mode != "auto" || tag == "" || s.Now().UTC().Hour() != s.updHour() || s.installBusy() {
		return
	}
	if err := s.startUpdate(tag); err == nil {
		s.Store.Audit(0, "update.auto", s.Version+" -> "+tag)
	}
}
