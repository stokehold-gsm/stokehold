package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/store"
)

var timeRE = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

type schedRow struct {
	ID   int64
	Cmd  string
	When string
	Last string
	OK   bool
	Note string
}

func (s *Server) loadServer(r *http.Request) *store.Server {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil
	}
	list, _ := s.Store.Servers()
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func (s *Server) fillSchedules(p *page, sv *store.Server) {
	p.ServerName, p.ServerID = sv.Name, sv.ID
	list, _ := s.Store.SchedulesFor(sv.ID)
	p.CanControl = canControl(p.User.Role)
	for _, x := range list {
		row := schedRow{ID: x.ID, Cmd: x.Cmd, OK: x.LastOK, Note: x.LastNote}
		if x.Kind == store.KindDaily {
			row.When = fmt.Sprintf(tr(p.Lang, "sch_when_daily"), x.Value/60, x.Value%60)
		} else {
			row.When = fmt.Sprintf(tr(p.Lang, "sch_when_interval"), x.Value)
		}
		if x.LastRun > 0 && x.LastNote != "" {
			row.Last = time.Unix(x.LastRun, 0).UTC().Format("2006-01-02 15:04") + " UTC"
		} else {
			row.Last = tr(p.Lang, "sch_never")
		}
		p.Sched = append(p.Sched, row)
	}
}

func (s *Server) schedulesPage(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.loadServer(r)
	if sv == nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	s.fillSchedules(p, sv)
	s.render(w, "schedules", p, http.StatusOK)
}

func (s *Server) scheduleAdd(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	sv := s.loadServer(r)
	if sv == nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	cmd, kind := r.PostFormValue("cmd"), r.PostFormValue("kind")
	value := -1
	switch kind {
	case store.KindDaily:
		if m := timeRE.FindStringSubmatch(r.PostFormValue("time")); m != nil {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			value = h*60 + mi
		}
	case store.KindInterval:
		if h, err := strconv.Atoi(r.PostFormValue("hours")); err == nil {
			value = h
		}
	}
	if _, err := s.Store.AddSchedule(sv.ID, cmd, kind, value, s.Now()); err != nil {
		p.Error = tr(p.Lang, "sch_invalid")
		s.fillSchedules(p, sv)
		s.render(w, "schedules", p, http.StatusBadRequest)
		return
	}
	s.Store.Audit(p.User.ID, "schedule.add", fmt.Sprintf("%s %s %s %d", sv.Name, cmd, kind, value))
	http.Redirect(w, r, fmt.Sprintf("/servers/%d/schedules", sv.ID), http.StatusSeeOther)
}

func (s *Server) scheduleDelete(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || s.Store.DeleteSchedule(id) != nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	s.Store.Audit(p.User.ID, "schedule.delete", strconv.FormatInt(id, 10))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
