package web

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stokehold-gsm/stokehold/internal/helper"
	"github.com/stokehold-gsm/stokehold/internal/safefs"
	"github.com/stokehold-gsm/stokehold/internal/store"
)

// File manager for a game server's directory. Everything goes through the
// helper's confined file operations (see internal/helper/fm.go); this file
// only validates input, pages the data and keeps the audit log.
const (
	// FMMaxUpload is the most one upload request may carry (all files together).
	FMMaxUpload = 256 << 20
	fmMaxPaths  = 200
)

type fmCrumb struct{ Name, Href string }

type fmRow struct {
	Name, Href, EditHref, DownloadHref, RenameHref, DeleteHref string
	Path, Icon, Size, Time                                     string
	IsDir                                                      bool
}

type fmView struct {
	Dir                          string // "" for the server's home
	Crumbs                       []fmCrumb
	Rows                         []fmRow
	More                         bool
	Up                           string
	NameLink, SizeLink, TimeLink string
	NameMark, SizeMark, TimeMark string
	Configs                      []string
	Confirm                      []string // paths waiting for a delete confirmation
	From                         string   // rename: the current path
	Max                          int      // upload cap in MB
	Back                         string
	Base                         string // /servers/{id}
}

func (s *Server) fmRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /servers/{id}/fm/mkdir", s.auth(s.fmMkdir))
	mux.HandleFunc("POST /servers/{id}/fm/touch", s.auth(s.fmTouch))
	mux.HandleFunc("POST /servers/{id}/fm/upload", s.fmUpload) // reads its own CSRF field: the body is streamed
	mux.HandleFunc("GET /servers/{id}/fm/download", s.auth(s.fmDownload))
	mux.HandleFunc("GET /servers/{id}/fm/rename", s.auth(s.fmRenameForm))
	mux.HandleFunc("POST /servers/{id}/fm/rename", s.auth(s.fmRename))
	mux.HandleFunc("GET /servers/{id}/fm/delete", s.auth(s.fmDeleteForm))
	mux.HandleFunc("POST /servers/{id}/fm/delete", s.auth(s.fmDelete))
}

// fmClean turns a path from a form or the address bar into a clean relative
// one ("." is the home); false means it is not acceptable.
func fmClean(p string) (string, bool) {
	if p == "" {
		return ".", true
	}
	if len(p) > 400 || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return "", false
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// fmName validates one file or folder name typed by the user.
func fmName(n string) bool {
	if n == "" || len(n) > 200 || n == "." || n == ".." || strings.ContainsAny(n, "/\\\x00") {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func fmJoin(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

func fmParent(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// fmServer finds the server and checks the role; it answers the error page itself.
func (s *Server) fmServer(w http.ResponseWriter, r *http.Request, p *page) *store.Server {
	if !canControl(p.User.Role) {
		s.denied(w, p)
		return nil
	}
	sv := s.loadServer(r)
	if sv == nil {
		s.render(w, "message", &page{Lang: p.Lang, User: p.User, CSRF: p.CSRF, Error: tr(p.Lang, "not_found")}, http.StatusNotFound)
	}
	return sv
}

func (s *Server) fmCall(sv *store.Server, req helper.Request) (helper.Response, error) {
	req.User = sv.LinuxUser
	return s.callHelperOn(sv.NodeID, req)
}

func base(sv *store.Server) string { return "/servers/" + strconv.FormatInt(sv.ID, 10) }

func fmURL(sv *store.Server, tail, p string, extra ...string) string {
	q := url.Values{}
	if p != "" && p != "." {
		q.Set("path", p)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	u := base(sv) + tail
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

var fmOK = map[string]bool{"mkdir": true, "touch": true, "upload": true, "rename": true, "delete": true}

// filesPage is the file manager's browser; it replaces the old flat list of configs.
func (s *Server) filesPage(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	dir, ok := fmClean(r.URL.Query().Get("path"))
	if !ok {
		dir = "."
	}
	s.fmBrowse(w, r, p, sv, dir, http.StatusOK)
}

func (s *Server) fmBrowse(w http.ResponseWriter, r *http.Request, p *page, sv *store.Server, dir string, status int) {
	p.ServerName, p.ServerID = sv.Name, sv.ID
	q := r.URL.Query()
	if p.Info == "" {
		if k := q.Get("ok"); fmOK[k] {
			p.Info = tr(p.Lang, "fm_ok_"+k)
		} else if q.Get("saved") != "" {
			p.Info = tr(p.Lang, "file_saved")
		}
	}
	v := &fmView{Base: base(sv), Max: FMMaxUpload >> 20}
	if dir != "." {
		v.Dir, v.Up = dir, fmParent(dir)
	}
	v.Crumbs = []fmCrumb{{Name: "/", Href: fmURL(sv, "/files", ".")}}
	for i, part := range strings.Split(v.Dir, "/") {
		if part == "" {
			continue
		}
		v.Crumbs = append(v.Crumbs, fmCrumb{Name: part, Href: fmURL(sv, "/files", strings.Join(strings.Split(v.Dir, "/")[:i+1], "/"))})
	}
	p.FM = v
	resp, err := s.fmCall(sv, helper.Request{Op: "list-dir", Path: dir})
	if err != nil {
		p.Error = tr(p.Lang, "helper_down")
		s.render(w, "fm", p, http.StatusBadGateway)
		return
	}
	var list struct {
		List []safefs.Entry `json:"list"`
		More bool           `json:"more"`
	}
	if !resp.OK || json.Unmarshal([]byte(resp.Out), &list) != nil {
		if p.Error == "" {
			p.Error = tr(p.Lang, "fm_nodir")
		}
		s.render(w, "fm", p, http.StatusNotFound)
		return
	}
	v.More = list.More
	sortKey, order := q.Get("sort"), q.Get("order")
	if sortKey != "size" && sortKey != "time" {
		sortKey = "name"
	}
	desc := order == "desc"
	sort.SliceStable(list.List, func(i, j int) bool {
		a, b := list.List[i], list.List[j]
		if (a.Type == "d") != (b.Type == "d") {
			return a.Type == "d"
		}
		var less bool
		switch sortKey {
		case "size":
			less = a.Size < b.Size
		case "time":
			less = a.Mtime < b.Mtime
		default:
			less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if a.Size == b.Size && sortKey == "size" || a.Mtime == b.Mtime && sortKey == "time" {
			less = strings.ToLower(a.Name) < strings.ToLower(b.Name)
			return less
		}
		return less != desc
	})
	link := func(key string) string {
		o := "asc"
		if sortKey == key && !desc {
			o = "desc"
		}
		return fmURL(sv, "/files", dir, "sort", key, "order", o)
	}
	mark := func(key string) string {
		switch {
		case sortKey != key:
			return ""
		case desc:
			return "▼"
		}
		return "▲"
	}
	v.NameLink, v.SizeLink, v.TimeLink = link("name"), link("size"), link("time")
	v.NameMark, v.SizeMark, v.TimeMark = mark("name"), mark("size"), mark("time")
	for _, e := range list.List {
		full := fmJoin(dir, e.Name)
		row := fmRow{Name: e.Name, Path: full, Icon: "file", Time: time.Unix(e.Mtime, 0).UTC().Format("2006-01-02 15:04"),
			RenameHref: fmURL(sv, "/fm/rename", full), DeleteHref: fmURL(sv, "/fm/delete", full)}
		switch e.Type {
		case "d":
			row.IsDir, row.Icon = true, "folder"
			row.Href = fmURL(sv, "/files", full)
		case "l":
			row.Icon = "link"
		case "f":
			row.Size = humanSize(e.Size)
			row.DownloadHref = fmURL(sv, "/fm/download", full)
			if helper.CanEdit(full) && e.Size <= safefs.MaxEditSize {
				row.EditHref = base(sv) + "/edit?path=" + url.QueryEscape(full)
			}
		default:
			row.Icon = "file"
		}
		v.Rows = append(v.Rows, row)
	}
	if dir == "." {
		if resp, err := s.fmCall(sv, helper.Request{Op: "files"}); err == nil && resp.OK {
			for _, f := range strings.Split(resp.Out, "\n") {
				if f = strings.TrimSpace(f); f != "" {
					v.Configs = append(v.Configs, f)
				}
			}
		}
	}
	s.render(w, "fm", p, status)
}

// fmFail shows the folder again with an error above it.
func (s *Server) fmFail(w http.ResponseWriter, r *http.Request, p *page, sv *store.Server, dir, msgKey string, status int) {
	p.Error = tr(p.Lang, msgKey)
	s.fmBrowse(w, r, p, sv, dir, status)
}

func (s *Server) fmDone(w http.ResponseWriter, r *http.Request, sv *store.Server, dir, ok string) {
	http.Redirect(w, r, fmURL(sv, "/files", dir, "ok", ok), http.StatusSeeOther)
}

func (s *Server) fmMkdir(w http.ResponseWriter, r *http.Request, p *page) {
	s.fmCreate(w, r, p, "mkdir")
}

func (s *Server) fmTouch(w http.ResponseWriter, r *http.Request, p *page) {
	s.fmCreate(w, r, p, "touch")
}

func (s *Server) fmCreate(w http.ResponseWriter, r *http.Request, p *page, kind string) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	dir, ok := fmClean(r.PostFormValue("path"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if !ok {
		dir = "."
	}
	if !ok || !fmName(name) {
		s.fmFail(w, r, p, sv, dir, "fm_badname", http.StatusBadRequest)
		return
	}
	target := fmJoin(dir, name)
	var resp helper.Response
	var err error
	if kind == "mkdir" {
		resp, err = s.fmCall(sv, helper.Request{Op: "mkdir", Path: target})
	} else if st, serr := s.fmCall(sv, helper.Request{Op: "stat", Path: target}); serr == nil && st.OK {
		s.fmFail(w, r, p, sv, dir, "fm_exists", http.StatusConflict)
		return
	} else {
		resp, err = s.fmCall(sv, helper.Request{Op: "put", Path: target, Final: true})
	}
	if err != nil {
		s.fmFail(w, r, p, sv, dir, "helper_down", http.StatusBadGateway)
		return
	}
	if !resp.OK {
		s.fmFail(w, r, p, sv, dir, "fm_failed", http.StatusBadRequest)
		return
	}
	s.Store.Audit(p.User.ID, "file."+kind, sv.Name+" "+target)
	s.fmDone(w, r, sv, dir, kind)
}

// fmUpload streams a multipart body to the helper in chunks, so a large file
// never sits in the panel's memory or temp directory. The form puts the CSRF
// field first, then the folder, then the files.
func (s *Server) fmUpload(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	u, csrf, err := s.Store.SessionUser(c.Value)
	if err != nil {
		s.clearCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	p := &page{Lang: lang(w, r), User: u, CSRF: csrf}
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	rc := http.NewResponseController(w)
	rc.SetReadDeadline(time.Time{}) // the server-wide timeouts are for pages, not for gigabytes
	rc.SetWriteDeadline(time.Time{})
	r.Body = http.MaxBytesReader(w, r.Body, FMMaxUpload+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		s.fmFail(w, r, p, sv, ".", "fm_upload_bad", http.StatusBadRequest)
		return
	}
	var (
		authed bool
		dir    = "."
		saved  int
		failed bool
		tooBig bool
	)
	for {
		part, err := mr.NextPart()
		if err != nil {
			tooBig = tooBig || isTooLarge(err)
			break
		}
		switch part.FormName() {
		case "csrf":
			b, _ := io.ReadAll(io.LimitReader(part, 256))
			authed = subtle.ConstantTimeCompare(b, []byte(csrf)) == 1
		case "path":
			b, _ := io.ReadAll(io.LimitReader(part, 512))
			if d, ok := fmClean(string(b)); ok {
				dir = d
			}
		case "file":
			if !authed {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
			if !fmName(name) {
				if name != "." && part.FileName() != "" {
					failed = true
				}
				continue
			}
			if err := s.fmStream(sv, fmJoin(dir, name), part); err != nil {
				failed = true
				tooBig = tooBig || isTooLarge(err)
				log.Printf("upload %s/%s: %v", sv.Name, name, err)
				continue
			}
			saved++
			s.Store.Audit(u.ID, "file.upload", sv.Name+" "+fmJoin(dir, name))
		}
	}
	if !authed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch {
	case tooBig:
		s.fmFail(w, r, p, sv, dir, "fm_upload_big", http.StatusRequestEntityTooLarge)
	case failed:
		s.fmFail(w, r, p, sv, dir, "fm_failed", http.StatusBadRequest)
	case saved == 0:
		s.fmFail(w, r, p, sv, dir, "fm_upload_none", http.StatusBadRequest)
	default:
		s.fmDone(w, r, sv, dir, "upload")
	}
}

func isTooLarge(err error) bool {
	var mb *http.MaxBytesError
	return errors.As(err, &mb)
}

// fmStream sends one file to the helper, a chunk at a time. The next chunk is
// read before the current one is sent so the last one can be marked final.
func (s *Server) fmStream(sv *store.Server, target string, src io.Reader) error {
	read := func() ([]byte, error) {
		buf := make([]byte, safefs.MaxChunk)
		n, err := io.ReadFull(src, buf)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			err = nil
		}
		return buf[:n], err
	}
	abort := func() { s.fmCall(sv, helper.Request{Op: "delete", Path: target + safefs.UploadTmp}) }
	cur, err := read()
	if err != nil {
		return err
	}
	for offset := int64(0); ; {
		next, err := read()
		if err != nil {
			abort()
			return err
		}
		final := len(next) == 0
		resp, err := s.fmCall(sv, helper.Request{Op: "put", Path: target, Offset: offset, Data: base64.StdEncoding.EncodeToString(cur), Final: final})
		if err != nil {
			return err
		}
		if !resp.OK {
			abort()
			return errors.New(resp.Err)
		}
		if final {
			return nil
		}
		offset += int64(len(cur))
		cur = next
	}
}

func (s *Server) fmDownload(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	file, ok := fmClean(r.URL.Query().Get("path"))
	if !ok || file == "." {
		http.NotFound(w, r)
		return
	}
	resp, err := s.fmCall(sv, helper.Request{Op: "stat", Path: file})
	var st safefs.Entry
	if err != nil || !resp.OK || json.Unmarshal([]byte(resp.Out), &st) != nil || st.Type != "f" {
		http.NotFound(w, r)
		return
	}
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(file)}))
	h.Set("Content-Length", strconv.FormatInt(st.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	s.Store.Audit(p.User.ID, "file.download", sv.Name+" "+file)
	for offset := int64(0); offset < st.Size; {
		resp, err := s.fmCall(sv, helper.Request{Op: "get", Path: file, Offset: offset, Length: safefs.MaxChunk})
		if err != nil || !resp.OK {
			log.Printf("download %s %s: stopped at %d", sv.Name, file, offset)
			return // headers are gone; closing the connection is the only honest signal
		}
		b, derr := base64.StdEncoding.DecodeString(resp.Out)
		if derr != nil || len(b) == 0 {
			return
		}
		if int64(len(b)) > st.Size-offset {
			b = b[:st.Size-offset]
		}
		if _, err := w.Write(b); err != nil {
			return
		}
		offset += int64(len(b))
		rc.Flush()
	}
}

func (s *Server) fmRenameForm(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	from, ok := fmClean(r.URL.Query().Get("path"))
	if !ok || from == "." {
		http.NotFound(w, r)
		return
	}
	p.ServerName, p.ServerID = sv.Name, sv.ID
	p.FM = &fmView{Base: base(sv), From: from, Back: fmURL(sv, "/files", fmParent(from))}
	s.render(w, "fmrename", p, http.StatusOK)
}

func (s *Server) fmRename(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	from, ok1 := fmClean(r.PostFormValue("path"))
	to, ok2 := fmClean(strings.TrimSpace(r.PostFormValue("to")))
	if !ok1 || !ok2 || from == "." || to == "." {
		s.fmFail(w, r, p, sv, ".", "fm_badname", http.StatusBadRequest)
		return
	}
	dir := fmParent(from)
	resp, err := s.fmCall(sv, helper.Request{Op: "rename", Path: from, To: to})
	if err != nil {
		s.fmFail(w, r, p, sv, dir, "helper_down", http.StatusBadGateway)
		return
	}
	if !resp.OK {
		s.fmFail(w, r, p, sv, dir, "fm_failed", http.StatusBadRequest)
		return
	}
	s.Store.Audit(p.User.ID, "file.rename", sv.Name+" "+from+" -> "+to)
	s.fmDone(w, r, sv, fmParent(to), "rename")
}

func (s *Server) fmDeleteForm(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	target, ok := fmClean(r.URL.Query().Get("path"))
	if !ok || target == "." {
		http.NotFound(w, r)
		return
	}
	s.fmConfirm(w, p, sv, []string{target})
}

func (s *Server) fmConfirm(w http.ResponseWriter, p *page, sv *store.Server, paths []string) {
	p.ServerName, p.ServerID = sv.Name, sv.ID
	// From carries the folder to come back to
	p.FM = &fmView{Base: base(sv), Confirm: paths, From: fmParent(paths[0]), Back: fmURL(sv, "/files", fmParent(paths[0]))}
	s.render(w, "fmdelete", p, http.StatusOK)
}

// fmDelete asks once, then deletes. A selection from the list (or one link)
// comes without "confirmed" and gets the confirmation page.
func (s *Server) fmDelete(w http.ResponseWriter, r *http.Request, p *page) {
	sv := s.fmServer(w, r, p)
	if sv == nil {
		return
	}
	r.ParseForm()
	var paths []string
	seen := map[string]bool{}
	for _, raw := range r.PostForm["paths"] {
		c, ok := fmClean(raw)
		if !ok || c == "." {
			s.fmFail(w, r, p, sv, ".", "fm_badname", http.StatusBadRequest)
			return
		}
		if !seen[c] {
			seen[c] = true
			paths = append(paths, c)
		}
	}
	dir, _ := fmClean(r.PostFormValue("dir"))
	if len(paths) == 0 {
		s.fmFail(w, r, p, sv, dir, "fm_nothing", http.StatusBadRequest)
		return
	}
	if len(paths) > fmMaxPaths {
		s.fmFail(w, r, p, sv, dir, "fm_toomany", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("confirmed") != "1" {
		s.fmConfirm(w, p, sv, paths)
		return
	}
	failed := 0
	for _, target := range paths {
		resp, err := s.fmCall(sv, helper.Request{Op: "delete", Path: target})
		if err != nil {
			s.fmFail(w, r, p, sv, dir, "helper_down", http.StatusBadGateway)
			return
		}
		if !resp.OK {
			failed++
			continue
		}
		s.Store.Audit(p.User.ID, "file.delete", sv.Name+" "+target)
	}
	if failed > 0 {
		s.fmFail(w, r, p, sv, dir, "fm_failed", http.StatusBadRequest)
		return
	}
	s.fmDone(w, r, sv, dir, "delete")
}
