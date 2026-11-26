package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/singularityos-lab/sinty-cloud/internal/files"
	"github.com/singularityos-lab/sinty-identity/rp"
)

//go:embed app.html
var appHTML []byte

//go:embed app.js
var appJS []byte

type Auth = rp.Authenticator

type Server struct {
	Files *files.Store
	UI    string
	Web   *rp.Web
}

func New(auth Auth, store *files.Store, ui string, secure bool) *Server {
	return &Server{Files: store, UI: ui, Web: rp.NewWeb(auth, "sinty_cloud", secure, files.Owner)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.Web.Login)
	mux.HandleFunc("GET /cb", s.Web.Callback)
	mux.HandleFunc("POST /logout", s.Web.Logout)
	mux.HandleFunc("GET /{$}", s.app)
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(appJS)
	})
	mux.HandleFunc("GET /api/v1/me", s.api(s.me))
	mux.HandleFunc("GET /api/v1/files/{path...}", s.api(s.get))
	mux.HandleFunc("GET /api/v1/info/{path...}", s.api(s.info))
	mux.HandleFunc("PUT /api/v1/files/{path...}", s.api(s.put))
	mux.HandleFunc("DELETE /api/v1/files/{path...}", s.api(s.remove))
	mux.HandleFunc("POST /api/v1/folders/{path...}", s.api(s.mkdir))
	mux.HandleFunc("POST /api/v1/move", s.api(s.move))
	mux.HandleFunc("POST /api/v1/copy", s.api(s.copy))
	mux.HandleFunc("GET /api/v1/preview/{path...}", s.api(s.preview))
	mux.HandleFunc("GET /api/v1/search", s.api(s.search))
	mux.HandleFunc("GET /api/v1/recent", s.api(s.recent))
	mux.HandleFunc("GET /api/v1/stars", s.api(s.stars))
	mux.HandleFunc("PUT /api/v1/stars/{path...}", s.api(s.star(true)))
	mux.HandleFunc("DELETE /api/v1/stars/{path...}", s.api(s.star(false)))
	mux.HandleFunc("GET /api/v1/trash", s.api(s.trashList))
	mux.HandleFunc("POST /api/v1/trash/{path...}", s.api(s.trash))
	mux.HandleFunc("POST /api/v1/restore/{id}", s.api(s.restore))
	mux.HandleFunc("DELETE /api/v1/trash/{id}", s.api(s.purge))
	mux.HandleFunc("DELETE /api/v1/trash", s.api(s.emptyTrash))
	mux.HandleFunc("GET /api/v1/links", s.api(s.links))
	mux.HandleFunc("POST /api/v1/links", s.api(s.share))
	mux.HandleFunc("DELETE /api/v1/links/{token}", s.api(s.unshare))
	mux.HandleFunc("GET /s/{token}", s.public)
	if s.UI != "" {
		mux.Handle("GET /ui/", http.StripPrefix("/ui/", http.FileServer(http.Dir(s.UI))))
	}
	return rp.SecureHeaders(mux)
}

func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.Web.Page(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(appHTML)
}

type handler func(w http.ResponseWriter, r *http.Request, user string)

func (s *Server) api(h handler) http.HandlerFunc {
	return s.Web.API(func(w http.ResponseWriter, r *http.Request, id rp.Identity) {
		h(w, r, id.User)
	})
}

var (
	fail = rp.Fail
	ok   = rp.Reply
)

func failFor(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrQuota):
		fail(w, http.StatusInsufficientStorage, "full")
	case errors.Is(err, files.ErrChanged):
		fail(w, http.StatusPreconditionFailed, "changed")
	case errors.Is(err, files.ErrName):
		fail(w, http.StatusBadRequest, "invalid_name")
	case errors.Is(err, fs.ErrNotExist):
		fail(w, http.StatusNotFound, "not_found")
	case errors.Is(err, fs.ErrExist):
		fail(w, http.StatusConflict, "exists")
	case errors.Is(err, files.ErrNotFile):
		fail(w, http.StatusBadRequest, "not_a_file")
	case errors.Is(err, files.ErrExpired):
		fail(w, http.StatusGone, "expired")
	default:
		fail(w, http.StatusInternalServerError, "server_error")
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, user string) {
	used, err := s.Files.Usage(user)
	if err != nil {
		failFor(w, err)
		return
	}
	var trashed int64
	items, _ := s.Files.TrashList(user)
	for _, it := range items {
		trashed += it.Size
	}
	ok(w, map[string]any{"user": user, "used": used, "quota": s.Files.Quota, "trash": trashed, "trash_items": len(items)})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, user string) {
	p := r.PathValue("path")
	info, err := s.Files.Stat(user, p)
	if err != nil {
		failFor(w, err)
		return
	}
	if info.IsDir() {
		entries, err := s.Files.List(user, p)
		if err != nil {
			failFor(w, err)
			return
		}
		dir := strings.Trim(path.Clean("/"+p), "/")
		stars, _ := s.Files.StarSet(user)
		shared := s.Files.SharedSet(user)
		for i := range entries {
			full := path.Join(dir, entries[i].Name)
			entries[i].Starred = stars[full]
			entries[i].Shared = shared[full]
		}
		ok(w, map[string]any{"path": "/" + dir, "entries": entries})
		return
	}
	if !info.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "not_found")
		return
	}
	f, err := s.Files.Open(user, p)
	if err != nil {
		failFor(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("ETag", files.ETag(info))
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) info(w http.ResponseWriter, r *http.Request, user string) {
	info, err := s.Files.Stat(user, r.PathValue("path"))
	if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
		failFor(w, fs.ErrNotExist)
		return
	}
	e := files.EntryOf(info)
	if strings.Trim(path.Clean("/"+r.PathValue("path")), "/") == "" {
		e.Name = ""
	}
	ok(w, e)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, user string) {
	e, err := s.Files.Put(user, r.PathValue("path"), r.Body, r.Header.Get("If-Match"))
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, e)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request, user string) {
	if err := s.Files.Remove(user, r.PathValue("path")); err != nil {
		failFor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request, user string) {
	if err := s.Files.Mkdir(user, r.PathValue("path")); err != nil {
		failFor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) move(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&in) != nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.Files.Move(user, in.From, in.To); err != nil {
		failFor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
