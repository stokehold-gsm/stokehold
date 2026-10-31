// Package catalog is the list of games LinuxGSM can install. The CSV is a
// snapshot of LinuxGSM's own lgsm/data/serverlist.csv (MIT licence, see
// docs/licenses.md); refresh it with each release.
package catalog

import (
	_ "embed"
	"encoding/csv"
	"sort"
	"strings"
	"sync"
)

//go:embed serverlist.csv
var raw string

type Game struct {
	Short  string // "mc"
	Script string // "mcserver"
	Name   string // "Minecraft"
}

var (
	once  sync.Once
	games []Game
)

func load() {
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		panic("bad embedded serverlist.csv: " + err.Error()) // better than an empty catalogue
	}
	for i, row := range rows {
		if i == 0 || len(row) < 3 {
			continue
		}
		games = append(games, Game{Short: row[0], Script: row[1], Name: row[2]})
	}
	sort.Slice(games, func(i, j int) bool { return strings.ToLower(games[i].Name) < strings.ToLower(games[j].Name) })
}

// Games returns every installable game, sorted by display name.
func Games() []Game {
	once.Do(load)
	return games
}

// Find returns the game for a script name such as "mcserver".
func Find(script string) (Game, bool) {
	for _, g := range Games() {
		if g.Script == script {
			return g, true
		}
	}
	return Game{}, false
}
