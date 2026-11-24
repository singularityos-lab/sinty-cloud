package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/singularityos-lab/sinty-cloud/internal/files"
	"github.com/singularityos-lab/sinty-identity/rp"
)

const (
	sessionCookie = "sinty_cloud"
	loginCookie   = "sinty_cloud_login"
	sessionTTL    = 8 * time.Hour
	loginTTL      = 10 * time.Minute
	requestHeader = "X-Sinty-Request"
)

//go:embed app.html
var appHTML []byte

//go:embed app.js
var appJS []byte

type Auth interface {
	Begin(acr ...string) rp.Request
	Finish(ctx context.Context, req rp.Request, answer url.Values) (rp.Claims, error)
	Access(ctx context.Context, bearer string) (rp.Claims, error)
}

type session struct {
	user    string
	expires time.Time
}

type pending struct {
	req     rp.Request
	expires time.Time
}

type Server struct {
	Auth   Auth
	Files  *files.Store
	UI     string
	Secure bool
	Now    func() time.Time

	mu       sync.Mutex
	sessions map[string]session
	logins   map[string]pending
}

func New(auth Auth, store *files.Store, ui string, secure bool) *Server {
	return &Server{Auth: auth, Files: store, UI: ui, Secure: secure, Now: time.Now, sessions: map[string]session{}, logins: map[string]pending{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("GET /cb", s.callback)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.app)
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(appJS)
	})
	mux.HandleFunc("GET /api/v1/me", s.api(s.me))
	mux.HandleFunc("GET /api/v1/files/{path...}", s.api(s.get))
	mux.HandleFunc("PUT /api/v1/files/{path...}", s.api(s.put))
	mux.HandleFunc("DELETE /api/v1/files/{path...}", s.api(s.remove))
	mux.HandleFunc("POST /api/v1/folders/{path...}", s.api(s.mkdir))
	mux.HandleFunc("POST /api/v1/move", s.api(s.move))
	if s.UI != "" {
		mux.Handle("GET /ui/", http.StripPrefix("/ui/", http.FileServer(http.Dir(s.UI))))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) cookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl / time.Second)})
}

func (s *Server) gc() {
	now := s.Now()
	for k, v := range s.sessions {
		if now.After(v.expires) {
			delete(s.sessions, k)
		}
	}
	for k, v := range s.logins {
		if now.After(v.expires) {
			delete(s.logins, k)
		}
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	req := s.Auth.Begin()
	s.mu.Lock()
	s.gc()
	s.logins[req.State] = pending{req: req, expires: s.Now().Add(loginTTL)}
	s.mu.Unlock()
	s.cookie(w, loginCookie, req.State, loginTTL)
	http.Redirect(w, r, req.URL, http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(loginCookie)
	if err != nil {
		http.Error(w, "This sign-in did not start here.", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p, ok := s.logins[c.Value]
	delete(s.logins, c.Value)
	s.mu.Unlock()
	s.cookie(w, loginCookie, "", -time.Second)
	if !ok || s.Now().After(p.expires) {
		http.Error(w, "This sign-in has expired.", http.StatusBadRequest)
		return
	}
	claims, err := s.Auth.Finish(r.Context(), p.req, r.URL.Query())
	if errors.Is(err, rp.ErrWeak) {
		http.Error(w, "This service needs a stronger sign-in.", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, "The sign-in was refused.", http.StatusUnauthorized)
		return
	}
	user, err := files.Owner(claims)
	if err != nil {
		http.Error(w, "This account cannot use the cloud.", http.StatusForbidden)
		return
	}
	id := token()
	s.mu.Lock()
	s.sessions[id] = session{user: user, expires: s.Now().Add(sessionTTL)}
	s.mu.Unlock()
	s.cookie(w, sessionCookie, id, sessionTTL)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(requestHeader) == "" {
		http.Error(w, "missing request header", http.StatusBadRequest)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	s.cookie(w, sessionCookie, "", -time.Second)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sessionUser(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[c.Value]
	if !ok || s.Now().After(v.expires) {
		return ""
	}
	return v.user
}

func (s *Server) app(w http.ResponseWriter, r *http.Request) {
	if s.sessionUser(r) == "" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(appHTML)
}

type handler func(w http.ResponseWriter, r *http.Request, user string)

func (s *Server) api(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user := ""
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			claims, err := s.Auth.Access(r.Context(), bearer)
			if err == nil {
				user, err = files.Owner(claims)
			}
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				fail(w, http.StatusUnauthorized, "invalid_token")
				return
			}
		} else {
			if r.Header.Get(requestHeader) == "" {
				fail(w, http.StatusBadRequest, "missing_request_header")
				return
			}
			user = s.sessionUser(r)
		}
		if user == "" {
			fail(w, http.StatusUnauthorized, "sign_in")
			return
		}
		h(w, r, user)
	}
}

func fail(w http.ResponseWriter, status int, e string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": e})
}

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func failFor(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrQuota):
		fail(w, http.StatusInsufficientStorage, "full")
	case errors.Is(err, files.ErrName):
		fail(w, http.StatusBadRequest, "invalid_name")
	case errors.Is(err, fs.ErrNotExist):
		fail(w, http.StatusNotFound, "not_found")
	case errors.Is(err, fs.ErrExist):
		fail(w, http.StatusConflict, "exists")
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
	ok(w, map[string]any{"user": user, "used": used, "quota": s.Files.Quota})
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
		ok(w, map[string]any{"path": "/" + strings.TrimPrefix(path.Clean("/"+p), "/"), "entries": entries})
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
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, user string) {
	n, err := s.Files.Put(user, r.PathValue("path"), r.Body)
	if err != nil {
		failFor(w, err)
		return
	}
	ok(w, map[string]any{"size": n})
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
