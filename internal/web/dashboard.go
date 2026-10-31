package web

import (
	"strconv"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/store"
)

// srvRow is one server on the list: what to show and which of the start,
// stop and restart buttons make sense for its last known state.
type srvRow struct {
	ID                            int64
	Name, Game, Mode, Node        string
	State                         string // started | stopped | unknown, also the badge class
	Players                       string // "3/10", empty when unknown
	CanStart, CanStop, CanRestart bool
}

func srvState(raw string) string {
	switch u := strings.ToUpper(strings.TrimSpace(raw)); {
	case u == "STARTED" || u == "ONLINE" || u == "RUNNING":
		return "started"
	case u == "STOPPED" || u == "OFFLINE":
		return "stopped"
	}
	return "unknown"
}

func srvRows(servers []store.Server, statuses map[int64]ServerStatus, nodes map[int64]string) []srvRow {
	rows := make([]srvRow, 0, len(servers))
	for _, sv := range servers {
		st := statuses[sv.ID]
		r := srvRow{ID: sv.ID, Name: sv.Name, Game: sv.Game, Mode: sv.Mode, Node: nodes[sv.NodeID], State: srvState(st.State)}
		if st.HasPlayers {
			r.Players = strconv.Itoa(st.Players) + "/" + strconv.Itoa(st.Max)
		}
		// an unknown state keeps every button: LinuxGSM will say what is true
		r.CanStart = r.State != "started"
		r.CanStop = r.State != "stopped"
		r.CanRestart = r.State != "stopped"
		rows = append(rows, r)
	}
	return rows
}
