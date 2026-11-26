package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/singularityos-lab/sinty-cloud/internal/files"
)

const (
	searchLimit  = 200
	recentLimit  = 50
	previewLimit = 32 << 20
	textLimit    = 1 << 20
)

var previewTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(v) != nil {
		fail(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func (s *Server) copy(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, e, err := s.Files.Copy(user, in.From, in.To)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, files.Found{Path: p, Entry: e})
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request, user string) {
	p := r.PathValue("path")
	info, err := s.Files.Stat(user, p)
	if err != nil {
		failFor(w, err)
		return
	}
	if !info.Mode().IsRegular() || info.Size() > previewLimit {
		fail(w, http.StatusUnsupportedMediaType, "no_preview")
		return
	}
	f, err := s.Files.Open(user, p)
	if err != nil {
		failFor(w, err)
		return
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	kind := http.DetectContentType(head[:n])
	switch {
	case previewTypes[kind]:
	case strings.HasPrefix(kind, "text/plain") && info.Size() <= textLimit:
		kind = "text/plain; charset=utf-8"
	default:
		fail(w, http.StatusUnsupportedMediaType, "no_preview")
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		failFor(w, err)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("ETag", files.ETag(info))
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, user string) {
	found, err := s.Files.Search(user, r.URL.Query().Get("q"), searchLimit)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, map[string]any{"results": s.mark(user, found)})
}

func (s *Server) recent(w http.ResponseWriter, r *http.Request, user string) {
	found, err := s.Files.Recent(user, recentLimit)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, map[string]any{"results": s.mark(user, found)})
}

func (s *Server) stars(w http.ResponseWriter, r *http.Request, user string) {
	found, err := s.Files.Stars(user)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, map[string]any{"results": s.mark(user, found)})
}

func (s *Server) mark(user string, found []files.Found) []files.Found {
	stars, _ := s.Files.StarSet(user)
	shared := s.Files.SharedSet(user)
	for i := range found {
		found[i].Starred = stars[found[i].Path]
		found[i].Shared = shared[found[i].Path]
	}
	return found
}

func (s *Server) star(on bool) handler {
	return func(w http.ResponseWriter, r *http.Request, user string) {
		if err := s.Files.Star(user, r.PathValue("path"), on); err != nil {
			failFor(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) trashList(w http.ResponseWriter, r *http.Request, user string) {
	items, err := s.Files.TrashList(user)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, map[string]any{"items": items, "keep_days": int(files.TrashKeep / (24 * time.Hour))})
}

func (s *Server) trash(w http.ResponseWriter, r *http.Request, user string) {
	item, err := s.Files.Trash(user, r.PathValue("path"))
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, item)
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request, user string) {
	p, e, err := s.Files.Restore(user, r.PathValue("id"))
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, files.Found{Path: p, Entry: e})
}

func (s *Server) purge(w http.ResponseWriter, r *http.Request, user string) {
	if err := s.Files.Purge(user, r.PathValue("id")); err != nil {
		failFor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) emptyTrash(w http.ResponseWriter, r *http.Request, user string) {
	if err := s.Files.EmptyTrash(user); err != nil {
		failFor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) links(w http.ResponseWriter, r *http.Request, user string) {
	list, err := s.Files.Links(user)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, map[string]any{"links": list})
}

func (s *Server) share(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Path string `json:"path"`
		Days int    `json:"days"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Days < 0 || in.Days > 365 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	l, err := s.Files.Share(user, in.Path, time.Duration(in.Days)*24*time.Hour)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, l)
}

func (s *Server) unshare(w http.ResponseWriter, r *http.Request, user string) {
	if err := s.Files.Unshare(user, r.PathValue("token")); err != nil {
		failFor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) public(w http.ResponseWriter, r *http.Request) {
	l, info, err := s.Files.Resolve(r.PathValue("token"))
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		if errors.Is(err, files.ErrExpired) {
			http.Error(w, "This link has expired.", http.StatusGone)
			return
		}
		http.Error(w, "This link does not exist.", http.StatusNotFound)
		return
	}
	f, err := s.Files.Open(l.User, l.Path)
	if err != nil {
		http.Error(w, "This link does not exist.", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, "", info.ModTime(), f)
}
