package web

// The server hub: statistics from the in-memory history, the console line
// that types into the game through LinuxGSM `send`, and the quick facts the
// servers list shows next to each server.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
)

// modGames are the scripts that have a mods/plugins manager. The Pro edition
// owns those pages; it may add games here from an init func.
var modGames = map[string]bool{
	"mcserver": true, "pmcserver": true, "fctrserver": true,
	"rustserver": true, "vhserver": true, "cs2server": true, "pzserver": true,
	"gmodserver": true, "scpslserver": true, "arkserver": true, "sdtdserver": true,
}

// hasMods is the template's question: is there a mods tab for this game here?
func hasMods(game string) bool { return proBuild && modGames[game] }

type hubStats struct {
	Players, CPU, Mem chart
	Timeline          []band
	Span              string // how much time the charts cover
	MaxPlayers        int
	Samples           int
}

func (s *Server) hubStats(lang string, id int64) hubStats {
	h := s.history(id)
	st := hubStats{Samples: len(h), Timeline: timeline(h)}
	if len(h) == 0 {
		return st
	}
	pl, cpu, mem := make([]float64, len(h)), make([]float64, len(h)), make([]float64, len(h))
	for i, x := range h {
		pl[i], cpu[i], mem[i] = -1, x.CPU, x.Mem
		if x.HasPlayers {
			pl[i] = float64(x.Players)
			st.MaxPlayers = max(st.MaxPlayers, x.Max)
		}
	}
	st.Players = series(pl, float64(st.MaxPlayers))
	st.CPU = series(cpu, 100)
	st.Mem = series(mem, 0)
	st.Span = ago(lang, h[len(h)-1].T.Sub(h[0].T))
	return st
}

// rowFacts fills what the servers list shows from the history.
func (s *Server) rowFacts(lang string, rows []srvRow) {
	now := s.Now()
	for i := range rows {
		r := &rows[i]
		r.Mods = hasMods(r.Game)
		h := s.history(r.ID)
		if len(h) == 0 {
			continue
		}
		last := h[len(h)-1]
		if t := since(h); !t.IsZero() && len(h) > 1 {
			r.Since = ago(lang, now.Sub(t))
		}
		if r.Players == "" && last.HasPlayers {
			r.Players = strconv.Itoa(last.Players) + "/" + strconv.Itoa(last.Max)
		}
		if last.HasPlayers && last.Max > 0 {
			r.PlayersPct = min(100, 100*last.Players/last.Max)
		}
		running := last.State == "started" || last.State == "starting"
		if running && last.CPU >= 0 {
			r.CPU = strconv.FormatFloat(last.CPU, 'f', 0, 64) + "%"
		}
		if running && last.Mem >= 0 {
			r.Mem = memText(last.Mem)
		}
		tail := h
		if len(tail) > 180 { // three hours is enough for a sparkline
			tail = tail[len(tail)-180:]
		}
		vals := make([]float64, len(tail))
		top := 0
		for j, x := range tail {
			vals[j] = -1
			if x.HasPlayers {
				vals[j] = float64(x.Players)
				top = max(top, x.Max)
			}
		}
		r.Spark = spark(vals, float64(top))
	}
}

func memText(mb float64) string {
	if mb >= 1024 {
		return strconv.FormatFloat(mb/1024, 'f', 1, 64) + " GB"
	}
	return strconv.FormatFloat(mb, 'f', 0, 64) + " MB"
}

// spark is a 100x24 polyline for the list.
func spark(vals []float64, top float64) string {
	c := series(vals, top)
	if !c.Has {
		return ""
	}
	var b strings.Builder
	for _, p := range strings.Fields(c.Line) {
		x, y, _ := strings.Cut(p, ",")
		fx, _ := strconv.ParseFloat(x, 64)
		fy, _ := strconv.ParseFloat(y, 64)
		fmt.Fprintf(&b, "%.1f,%.1f ", fx*100/chartW, 1+fy*22/chartH)
	}
	return strings.TrimSpace(b.String())
}

// serverSend types one console line into the game.
func (s *Server) serverSend(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.serverByID(r)
	if sv == nil || !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	back := "/servers/" + strconv.FormatInt(sv.ID, 10) + "?tab=console&sent="
	text := strings.TrimSpace(r.PostFormValue("text"))
	if !helper.ValidSendText(text) {
		http.Redirect(w, r, back+"bad", http.StatusSeeOther)
		return
	}
	if !s.sendLimit.Allow(strconv.FormatInt(p.User.ID, 10), s.Now()) {
		http.Redirect(w, r, back+"slow", http.StatusSeeOther)
		return
	}
	resp, err := s.callHelperOn(sv.NodeID, helper.Request{Op: "send", User: sv.LinuxUser, Game: sv.Game, Content: text})
	s.Store.Audit(p.User.ID, "server.send", sv.Name+": "+clipText(text, 80))
	if err != nil || !resp.OK {
		http.Redirect(w, r, back+"fail", http.StatusSeeOther)
		return
	}
	// give the game a moment, so the reply is in the log when the page comes back
	time.Sleep(sendSettle)
	http.Redirect(w, r, back+"ok", http.StatusSeeOther)
}

// sendSettle lets the game write its reply before the page reads the log again; tests set it to 0.
var sendSettle = 700 * time.Millisecond

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
