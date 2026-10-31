package web

// One server on one page: live state, the facts LinuxGSM prints, the ports,
// actions and the tail of its logs. The overview asks LinuxGSM for `details`
// (cached for a few seconds, so the auto-refresh does not hammer it).

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/lgsm"
	"github.com/stokehold-gsm/stokehold/internal/safefs"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

const (
	detailsTTL = 8 * time.Second
	logTail    = 48 << 10 // bytes read from the end of a log
	logLines   = 400
)

type detailsCache struct {
	mu sync.Mutex
	m  map[int64]cachedDetails
}

type cachedDetails struct {
	at  time.Time
	raw string
	ok  bool
}

type srvPage struct {
	ID, Name, Game, Mode, Node string
	NodeID                     int64
	Tab                        string // overview | logs
	State                      string // started | starting | stopped | unknown
	Players                    string
	Address                    string
	D                          lgsm.Details
	Raw                        string
	Err                        string
	CanControl                 bool
	CanStart, CanStop          bool
	Log                        string
	LogKind                    string // console | script
	LogPath                    string
	LogErr                     string
}

func (s *Server) srvRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /servers/{id}", s.auth(s.serverPage))
}

func (s *Server) serverByID(r *http.Request) *store.Server {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil
	}
	servers, _ := s.Store.Servers()
	for i := range servers {
		if servers[i].ID == id {
			return &servers[i]
		}
	}
	return nil
}

func (s *Server) details(sv *store.Server) (string, bool) {
	s.dcache.mu.Lock()
	if c, ok := s.dcache.m[sv.ID]; ok && time.Since(c.at) < detailsTTL {
		s.dcache.mu.Unlock()
		return c.raw, c.ok
	}
	s.dcache.mu.Unlock()
	resp, err := s.callHelperOn(sv.NodeID, helper.Request{Op: "run", User: sv.LinuxUser, Game: sv.Game, Cmd: "details"})
	raw, ok := resp.Out, err == nil && resp.OK
	s.dcache.mu.Lock()
	if s.dcache.m == nil {
		s.dcache.m = map[int64]cachedDetails{}
	}
	s.dcache.m[sv.ID] = cachedDetails{at: time.Now(), raw: raw, ok: ok}
	s.dcache.mu.Unlock()
	return raw, ok
}

func (s *Server) serverPage(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.serverByID(r)
	if sv == nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	v := &srvPage{ID: strconv.FormatInt(sv.ID, 10), Name: sv.Name, Game: sv.Game, Mode: sv.Mode, NodeID: sv.NodeID,
		Tab: "overview", CanControl: canControl(p.User.Role)}
	s.fillNodes(p)
	v.Node = p.NodeNames[sv.NodeID]
	if r.URL.Query().Get("tab") == "logs" && v.CanControl {
		v.Tab = "logs"
	}
	// the cached state first, so a failing `details` still shows something
	v.State = srvState(s.Statuses()[sv.ID].State)
	if st, ok := s.Statuses()[sv.ID]; ok && st.HasPlayers {
		v.Players = strconv.Itoa(st.Players) + "/" + strconv.Itoa(st.Max)
	}
	if v.Tab == "logs" {
		s.fillLog(v, sv, r.URL.Query().Get("log"))
	} else {
		raw, ok := s.details(sv)
		if !ok {
			v.Err = tr(p.Lang, "srv_details_failed")
		} else {
			v.D = lgsm.ParseDetails(raw)
			v.Raw = trim(raw)
			switch v.D.State() {
			case "STARTED":
				v.State = "started"
			case "STARTING":
				v.State = "starting"
			case "STOPPED":
				v.State = "stopped"
			}
			if v.D.HasPlayers {
				v.Players = strconv.Itoa(v.D.Players) + "/" + strconv.Itoa(v.D.Max)
			}
			v.Address = rowValue(v.D, "Server IP", "Server address")
		}
	}
	v.CanStart, v.CanStop = v.State != "started", v.State != "stopped"
	p.Srv = v
	p.Refresh = false
	s.render(w, "server", p, http.StatusOK)
}

func rowValue(d lgsm.Details, keys ...string) string {
	for _, sec := range d.Sections {
		for _, row := range sec.Rows {
			for _, k := range keys {
				if strings.EqualFold(row.K, k) && !row.Secret {
					return row.V
				}
			}
		}
	}
	return ""
}

// fillLog reads the end of the console or script log through the helper.
func (s *Server) fillLog(v *srvPage, sv *store.Server, kind string) {
	if kind != "script" {
		kind = "console"
	}
	v.LogKind = kind
	v.LogPath = "log/" + kind + "/" + sv.Game + "-" + kind + ".log"
	st, err := s.callHelperOn(sv.NodeID, helper.Request{Op: "stat", User: sv.LinuxUser, Path: v.LogPath})
	if err != nil || !st.OK {
		v.LogErr = "srv_log_missing"
		return
	}
	size := jsonSize(st.Out)
	off := int64(0)
	if size > logTail {
		off = size - logTail
	}
	resp, err := s.callHelperOn(sv.NodeID, helper.Request{Op: "get", User: sv.LinuxUser, Path: v.LogPath, Offset: off, Length: logTail})
	if err != nil || !resp.OK {
		v.LogErr = "srv_log_missing"
		return
	}
	raw, err := base64.StdEncoding.DecodeString(resp.Out)
	if err != nil {
		v.LogErr = "srv_log_missing"
		return
	}
	text := lgsm.Clean(strings.ToValidUTF8(string(raw), ""))
	lines := strings.Split(text, "\n")
	if off > 0 && len(lines) > 1 {
		lines = lines[1:] // the first line is cut in the middle
	}
	if len(lines) > logLines {
		lines = lines[len(lines)-logLines:]
	}
	v.Log = strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func jsonSize(out string) int64 {
	var e safefs.Entry
	if json.Unmarshal([]byte(out), &e) != nil {
		return 0
	}
	return e.Size
}
