//go:build !pro

package web

// The Free edition. Everything a licence unlocks (API, several machines, mod
// manager, Discord webhook, public status page) lives in the closed Pro
// edition, which replaces this file; see docs/open-core.md.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stokehold-gsm/stokehold/internal/helper"
)

const proBuild = false

type proPage struct{}

func (s *Server) proRoutes(mux *http.ServeMux) {}

func (s *Server) fillNodes(p *page) {}

// pickNode accepts only this machine.
func (s *Server) pickNode(r *http.Request) (int64, bool) {
	v := strings.TrimSpace(r.PostFormValue("node"))
	return 0, v == "" || v == "0"
}

func (s *Server) callNode(node int64, r helper.Request) (helper.Response, error) {
	return helper.Response{}, errors.New("several machines need the Pro edition")
}

func (s *Server) notify(text string) {}
