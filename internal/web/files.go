package web

import (
	"net/http"
	"strconv"
	"strings"

	"stokehold/internal/helper"
	"stokehold/internal/safefs"
)

// Config files can hold secrets such as RCON passwords, so only people who
// may control the server can see or change them.
func (s *Server) filesPage(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	sv := s.loadServer(r)
	if sv == nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	p.ServerName, p.ServerID = sv.Name, sv.ID
	resp, err := s.callHelper(helper.Request{Op: "files", User: sv.LinuxUser})
	if err != nil || !resp.OK {
		p.Error = tr(p.Lang, "helper_down")
	} else {
		for _, f := range strings.Split(resp.Out, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				p.Files = append(p.Files, f)
			}
		}
	}
	if r.URL.Query().Get("saved") != "" {
		p.Info = tr(p.Lang, "file_saved")
	}
	s.render(w, "files", p, http.StatusOK)
}

func (s *Server) editPage(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	sv := s.loadServer(r)
	path := r.URL.Query().Get("path")
	if sv == nil || path == "" {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	p.ServerName, p.ServerID, p.FilePath = sv.Name, sv.ID, path
	resp, err := s.callHelper(helper.Request{Op: "read-file", User: sv.LinuxUser, Path: path})
	switch {
	case err != nil:
		p.Error = tr(p.Lang, "helper_down")
		s.render(w, "edit", p, http.StatusBadGateway)
		return
	case !resp.OK:
		p.Error = tr(p.Lang, "file_failed")
		s.render(w, "edit", p, http.StatusNotFound)
		return
	}
	p.FileContent = resp.Out
	s.render(w, "edit", p, http.StatusOK)
}

func (s *Server) editSave(w http.ResponseWriter, r *http.Request, p *page) {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return
	}
	sv := s.loadServer(r)
	path := r.PostFormValue("path")
	if sv == nil || path == "" {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
		return
	}
	// browsers send textarea line breaks as CRLF, game configs expect LF
	content := strings.ReplaceAll(r.PostFormValue("content"), "\r\n", "\n")
	p.ServerName, p.ServerID, p.FilePath, p.FileContent = sv.Name, sv.ID, path, content
	if len(content) > safefs.MaxEditSize {
		p.Error = tr(p.Lang, "file_too_big")
		s.render(w, "edit", p, http.StatusRequestEntityTooLarge)
		return
	}
	resp, err := s.callHelper(helper.Request{Op: "write-file", User: sv.LinuxUser, Path: path, Content: content})
	if err != nil || !resp.OK {
		p.Error = tr(p.Lang, "file_failed")
		if err != nil {
			p.Error = tr(p.Lang, "helper_down")
		}
		s.render(w, "edit", p, http.StatusBadRequest)
		return
	}
	s.Store.Audit(p.User.ID, "file.edit", sv.Name+" "+path) // the content is never logged
	http.Redirect(w, r, "/servers/"+strconv.FormatInt(sv.ID, 10)+"/files?saved=1", http.StatusSeeOther)
}
