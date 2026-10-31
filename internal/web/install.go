package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stokehold-gsm/stokehold/internal/catalog"
	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

// One install runs at a time: downloads are large and apt takes a lock.
type jobTracker struct {
	mu      sync.Mutex
	next    int64
	jobs    map[int64]*job
	running bool
}

type job struct {
	ID     int64
	Name   string
	Game   string
	User   string
	Status string // running | done | failed
	Log    []string
	Start  time.Time
}

func (t *jobTracker) begin(name, game, user string) (*job, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return nil, false
	}
	if t.jobs == nil {
		t.jobs = map[int64]*job{}
	}
	t.next++
	j := &job{ID: t.next, Name: name, Game: game, User: user, Status: "running", Start: time.Now()}
	t.jobs[j.ID] = j
	t.running = true
	return j, true
}

func (t *jobTracker) logf(j *job, format string, args ...any) {
	t.mu.Lock()
	j.Log = append(j.Log, fmt.Sprintf(format, args...))
	t.mu.Unlock()
}

func (t *jobTracker) finish(j *job, ok bool) {
	t.mu.Lock()
	j.Status = "done"
	if !ok {
		j.Status = "failed"
	}
	t.running = false
	t.mu.Unlock()
}

// snapshot copies a job so rendering never races the goroutine writing to it.
func (t *jobTracker) snapshot(id int64) (job, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	j, ok := t.jobs[id]
	if !ok {
		return job{}, false
	}
	c := *j
	c.Log = append([]string(nil), j.Log...)
	return c, true
}

// slug keeps ASCII letters and digits only, since it becomes a Linux user name.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// accountFor picks a free gs_ account name for a server name.
func accountFor(name, short string, taken func(string) bool) string {
	base := slug(name)
	if base == "" {
		base = slug(short)
	}
	if len(base) > 18 {
		base = base[:18]
	}
	for i := 0; i < 1000; i++ {
		cand := "gs_" + base
		if i > 0 {
			cand += strconv.Itoa(i)
		}
		if !taken(cand) {
			return cand
		}
	}
	return "gs_" + base + "x"
}

func (s *Server) newServerForm(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	p.Games = catalog.Games()
	s.render(w, "newserver", p, http.StatusOK)
}

func (s *Server) newServer(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	p.Games = catalog.Games()
	fail := func(key string, status int) {
		p.Error = tr(p.Lang, key)
		s.render(w, "newserver", p, status)
	}
	game, ok := catalog.Find(r.PostFormValue("game"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if !ok {
		fail("install_bad_game", http.StatusBadRequest)
		return
	}
	if name == "" {
		name = game.Name
	}
	if utf8.RuneCountInString(name) > 60 {
		fail("install_bad_name", http.StatusBadRequest)
		return
	}
	existing, _ := s.Store.Servers()
	for _, e := range existing {
		if strings.EqualFold(e.Name, name) {
			fail("server_exists", http.StatusConflict)
			return
		}
	}
	user := accountFor(name, game.Short, func(u string) bool {
		for _, e := range existing {
			if e.LinuxUser == u {
				return true
			}
		}
		return false
	})
	j, started := s.jobs.begin(name, game.Script, user)
	if !started {
		fail("install_busy", http.StatusConflict)
		return
	}
	s.Store.Audit(p.User.ID, "server.install", name+" "+game.Script)
	go s.runInstall(j, game)
	http.Redirect(w, r, "/jobs/"+strconv.FormatInt(j.ID, 10), http.StatusSeeOther)
}

// runInstall creates the account, installs the packages LinuxGSM lists for
// the game and installs the game. Every step is shown to the user as it goes.
func (s *Server) runInstall(j *job, game catalog.Game) {
	t := &s.jobs
	step := func(n int, what string, req helper.Request) bool {
		t.logf(j, "[%d/3] %s ...", n, what)
		resp, err := s.callHelper(req)
		switch {
		case err != nil:
			t.logf(j, "    failed: %v", err)
			return false
		case !resp.OK:
			t.logf(j, "    failed: %s", resp.Err)
			if out := tail(resp.Out, 12); out != "" {
				t.logf(j, "%s", out)
			}
			return false
		}
		t.logf(j, "    ok")
		return true
	}
	if !step(1, "creating account "+j.User, helper.Request{Op: "create-user", User: j.User}) {
		t.finish(j, false)
		return
	}
	pkgs, known := helper.DepsFor(s.Distro, game.Short)
	switch {
	case !known:
		t.logf(j, "[2/3] packages: no list for %q, skipped. If LinuxGSM asks for packages, install them by hand.", s.Distro)
	case len(pkgs) == 0:
		t.logf(j, "[2/3] packages: none needed")
	default:
		if !step(2, "installing packages ("+strconv.Itoa(len(pkgs))+")", helper.Request{Op: "deps", Packages: pkgs}) {
			t.finish(j, false)
			return
		}
	}
	if !step(3, "installing "+game.Name+" with LinuxGSM (this can take a while)", helper.Request{Op: "install", User: j.User, Game: game.Script}) {
		t.finish(j, false)
		return
	}
	if _, err := s.Store.AddServer(store.Server{Name: j.Name, Game: game.Script, LinuxUser: j.User, Dir: "/home/" + j.User, Mode: "managed"}); err != nil {
		t.logf(j, "installed, but could not register the server: %v", err)
		t.finish(j, false)
		return
	}
	t.logf(j, "done. The server is in the list; before the first start LinuxGSM may ask you to accept the game's EULA (see the troubleshooting guide).")
	t.finish(j, true)
}

// tail returns the last n non-empty lines, cleaned, so a huge install log
// does not flood the page.
func tail(out string, n int) string {
	lines := strings.Split(strings.TrimSpace(trim(out)), "\n")
	var keep []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "fetching GitHub") {
			keep = append(keep, "    "+strings.TrimSpace(l))
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, "\n")
}

func (s *Server) jobPage(w http.ResponseWriter, r *http.Request, p *page) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	j, ok := s.jobs.snapshot(id)
	if err != nil || !ok || !canControl(p.User.Role) {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	p.ServerName, p.Action = j.Name, j.Status
	p.Result = strings.Join(j.Log, "\n")
	p.Refresh = j.Status == "running"
	s.render(w, "job", p, http.StatusOK)
}
