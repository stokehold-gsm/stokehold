package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/lgsm"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

type HelperFunc func(helper.Request) (helper.Response, error)

const maxOutput = 8 << 10

// Actions a viewer may trigger: read-only ones. Everything else needs admin.
var viewerActions = map[string]bool{"details": true, "check-update": true}

var actionLabels = []string{"details", "start", "stop", "restart", "backup", "check-update"}

func canControl(role string) bool { return role == store.RoleOwner || role == store.RoleAdmin }

func (s *Server) callHelper(r helper.Request) (helper.Response, error) {
	if s.Helper == nil {
		return helper.Response{}, errors.New("helper is not configured")
	}
	return s.Helper(r)
}

// режем вывод скрипта, чтобы страница не пухла
func trim(out string) string {
	out = strings.ToValidUTF8(lgsm.Clean(out), "")
	if len(out) > maxOutput {
		out = out[len(out)-maxOutput:]
		_, size := utf8.DecodeRuneInString(out)
		if size == 1 {
			out = out[1:]
		}
		out = "…\n" + out
	}
	return out
}

func (s *Server) addForm(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	s.render(w, "add", p, http.StatusOK)
}

// find asks the helper which LinuxGSM servers an account has. The helper only
// answers for gs_ accounts and for accounts the owner listed in adopt.conf.
func (s *Server) find(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	user := strings.TrimSpace(r.PostFormValue("user"))
	resp, err := s.callHelper(helper.Request{Op: "discover", User: user})
	if err != nil || !resp.OK {
		p.Error = tr(p.Lang, "find_failed")
		s.render(w, "add", p, http.StatusBadRequest)
		return
	}
	p.AddUser = user
	for _, name := range strings.Split(resp.Out, "\n") {
		if name = strings.TrimSpace(name); lgsm.ValidName(name) {
			p.Found = append(p.Found, name)
		}
	}
	if len(p.Found) == 0 {
		p.Error = tr(p.Lang, "nothing_found")
	}
	s.render(w, "add", p, http.StatusOK)
}

func (s *Server) addServer(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	user, game := strings.TrimSpace(r.PostFormValue("user")), strings.TrimSpace(r.PostFormValue("game"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > 60 {
		name = game
	}
	// Never trust the form: ask the helper again whether this account really has this server.
	resp, err := s.callHelper(helper.Request{Op: "discover", User: user})
	found := false
	if err == nil && resp.OK {
		for _, n := range strings.Split(resp.Out, "\n") {
			found = found || strings.TrimSpace(n) == game
		}
	}
	if !found || !lgsm.ValidName(game) {
		p.Error = tr(p.Lang, "find_failed")
		s.render(w, "add", p, http.StatusBadRequest)
		return
	}
	mode := "adopted"
	if strings.HasPrefix(user, "gs_") {
		mode = "managed"
	}
	_, err = s.Store.AddServer(store.Server{Name: name, Game: game, LinuxUser: user, Dir: "/home/" + user, Mode: mode})
	if errors.Is(err, store.ErrExists) {
		p.Error = tr(p.Lang, "server_exists")
		s.render(w, "add", p, http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.Store.Audit(p.User.ID, "server.add", name+" "+user+"/"+game)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) runAction(w http.ResponseWriter, r *http.Request, p *page) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	cmd := r.PostFormValue("cmd")
	deny := func(msg string, status int) {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, msg)}, status)
	}
	if err != nil {
		deny("not_found", http.StatusNotFound)
		return
	}
	if !lgsm.ValidCommand(cmd) || (!viewerActions[cmd] && !canControl(p.User.Role)) {
		deny("forbidden", http.StatusForbidden)
		return
	}
	servers, _ := s.Store.Servers()
	var sv *store.Server
	for i := range servers {
		if servers[i].ID == id {
			sv = &servers[i]
		}
	}
	if sv == nil {
		deny("not_found", http.StatusNotFound)
		return
	}
	// Backups and updates can take minutes; the server-wide write timeout is short.
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(12 * time.Minute))
	resp, err := s.callHelper(helper.Request{Op: "run", User: sv.LinuxUser, Game: sv.Game, Cmd: cmd})
	s.Store.Audit(p.User.ID, "server.run", sv.Name+" "+cmd)
	p.ServerName, p.Action = sv.Name, cmd
	switch {
	case err != nil:
		p.Error = tr(p.Lang, "helper_down")
	case !resp.OK:
		p.Error = tr(p.Lang, "action_failed")
		p.Result = trim(resp.Out)
	default:
		p.Result = trim(resp.Out)
	}
	s.render(w, "result", p, http.StatusOK)
}
