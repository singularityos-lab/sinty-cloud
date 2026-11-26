package files

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	ErrNotFile = errors.New("files: only files can be shared")
	ErrExpired = errors.New("files: the link expired")
)

var linkToken = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

type Link struct {
	Token   string     `json:"token"`
	User    string     `json:"user"`
	Path    string     `json:"path"`
	Created time.Time  `json:"created"`
	Expires *time.Time `json:"expires,omitempty"`
}

func (s *Store) links() (*os.Root, error) {
	dir := filepath.Join(s.Dir, "links")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

func (s *Store) allLinks(r *os.Root) []Link {
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return nil
	}
	out := []Link{}
	for _, e := range entries {
		token, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !linkToken.MatchString(token) {
			continue
		}
		data, err := fs.ReadFile(r.FS(), e.Name())
		if err != nil {
			continue
		}
		var l Link
		if json.Unmarshal(data, &l) != nil || l.Token != token {
			continue
		}
		if l.Expires != nil && time.Now().After(*l.Expires) {
			r.Remove(e.Name())
			continue
		}
		out = append(out, l)
	}
	return out
}

func (s *Store) editLinks(user string, edit func(string) (string, bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.links()
	if err != nil {
		return
	}
	defer r.Close()
	for _, l := range s.allLinks(r) {
		if l.User != user {
			continue
		}
		p, keep := edit(l.Path)
		switch {
		case !keep:
			r.Remove(l.Token + ".json")
		case p != l.Path:
			l.Path = p
			writeJSON(r, l.Token+".json", l)
		}
	}
}

func (s *Store) relink(links []Link, base string) {
	if len(links) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.links()
	if err != nil {
		return
	}
	defer r.Close()
	for _, l := range links {
		if l.Expires != nil && time.Now().After(*l.Expires) {
			continue
		}
		l.Path = base + l.Path
		writeJSON(r, l.Token+".json", l)
	}
}

func (s *Store) SharedSet(user string) map[string]bool {
	set := map[string]bool{}
	ls, _ := s.Links(user)
	for _, l := range ls {
		set[l.Path] = true
	}
	return set
}

func (s *Store) Links(user string) ([]Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.links()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := slices.DeleteFunc(s.allLinks(r), func(l Link) bool { return l.User != user })
	slices.SortFunc(out, func(a, b Link) int { return b.Created.Compare(a.Created) })
	return out, nil
}

func (s *Store) Share(user, p string, valid time.Duration) (Link, error) {
	c, err := clean(p)
	if err != nil || c == "." {
		return Link{}, ErrName
	}
	info, err := s.Stat(user, c)
	if err != nil {
		return Link{}, err
	}
	if !info.Mode().IsRegular() {
		return Link{}, ErrNotFile
	}
	b := make([]byte, 24)
	rand.Read(b)
	l := Link{Token: base64.RawURLEncoding.EncodeToString(b), User: user, Path: c, Created: time.Now().UTC()}
	if valid > 0 {
		t := l.Created.Add(valid)
		l.Expires = &t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.links()
	if err != nil {
		return Link{}, err
	}
	defer r.Close()
	if err := writeJSON(r, l.Token+".json", l); err != nil {
		return Link{}, err
	}
	return l, nil
}

func (s *Store) Unshare(user, token string) error {
	if !linkToken.MatchString(token) {
		return fs.ErrNotExist
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.links()
	if err != nil {
		return err
	}
	defer r.Close()
	data, err := fs.ReadFile(r.FS(), token+".json")
	if err != nil {
		return fs.ErrNotExist
	}
	var l Link
	if json.Unmarshal(data, &l) != nil || l.User != user {
		return fs.ErrNotExist
	}
	return r.Remove(token + ".json")
}

func (s *Store) Resolve(token string) (Link, fs.FileInfo, error) {
	if !linkToken.MatchString(token) {
		return Link{}, nil, fs.ErrNotExist
	}
	s.mu.Lock()
	r, err := s.links()
	if err != nil {
		s.mu.Unlock()
		return Link{}, nil, err
	}
	data, err := fs.ReadFile(r.FS(), token+".json")
	r.Close()
	s.mu.Unlock()
	if err != nil {
		return Link{}, nil, fs.ErrNotExist
	}
	var l Link
	if json.Unmarshal(data, &l) != nil || l.Token != token {
		return Link{}, nil, fs.ErrNotExist
	}
	if l.Expires != nil && time.Now().After(*l.Expires) {
		return Link{}, nil, ErrExpired
	}
	info, err := s.Stat(l.User, l.Path)
	if err != nil || !info.Mode().IsRegular() {
		return Link{}, nil, fs.ErrNotExist
	}
	return l, info, nil
}

func (l Link) Name() string {
	return path.Base(l.Path)
}
