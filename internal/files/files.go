package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/singularityos-lab/sinty-identity/rp"
)

var (
	ErrName    = errors.New("files: invalid name")
	ErrQuota   = errors.New("files: the space is full")
	ErrUser    = errors.New("files: invalid user")
	ErrChanged = errors.New("files: the file changed")
)

const (
	reserved = ".sinty-"
	partial  = reserved + "upload-"
	trashDir = reserved + "trash"
	metaDir  = reserved + "meta"
)

var userName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type Entry struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
	Starred  bool      `json:"starred,omitempty"`
	Shared   bool      `json:"shared,omitempty"`
}

func ETag(info fs.FileInfo) string {
	if info.IsDir() {
		return ""
	}
	var ino uint64
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		ino = st.Ino
	}
	return fmt.Sprintf(`"%x-%x-%x"`, ino, info.Size(), info.ModTime().UnixNano())
}

func EntryOf(info fs.FileInfo) Entry {
	return Entry{Name: info.Name(), Dir: info.IsDir(), Size: info.Size(), Modified: info.ModTime().UTC(), ETag: ETag(info)}
}

type Store struct {
	Dir   string
	Quota int64

	mu    sync.Mutex
	usage map[string]int64
	locks map[string]*sync.Mutex
}

func New(dir string, quota int64) *Store {
	return &Store{Dir: dir, Quota: quota, usage: map[string]int64{}, locks: map[string]*sync.Mutex{}}
}

func Owner(c rp.Claims) (string, error) {
	if !userName.MatchString(c.Subject) {
		return "", ErrUser
	}
	return c.Subject, nil
}

func clean(p string) (string, error) {
	c := strings.TrimPrefix(path.Clean("/"+p), "/")
	for _, part := range strings.Split(c, "/") {
		if strings.HasPrefix(part, reserved) || strings.ContainsRune(part, 0) {
			return "", ErrName
		}
	}
	if c == "" {
		c = "."
	}
	return c, nil
}

func (s *Store) lock(user string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.locks[user]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[user] = m
	}
	return m
}

func (s *Store) root(user string) (*os.Root, error) {
	if !userName.MatchString(user) {
		return nil, ErrUser
	}
	dir := filepath.Join(s.Dir, "users", user)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}

func (s *Store) List(user, p string) ([]Entry, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	items, err := fs.ReadDir(r.FS(), c)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, it := range items {
		if strings.HasPrefix(it.Name(), reserved) || it.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, EntryOf(info))
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

func (s *Store) Stat(user, p string) (fs.FileInfo, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Lstat(c)
}

func (s *Store) Open(user, p string) (*os.File, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Open(c)
}

func (s *Store) used(user string, r *os.Root) int64 {
	s.mu.Lock()
	n, ok := s.usage[user]
	s.mu.Unlock()
	if ok {
		return n
	}
	fs.WalkDir(r.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	s.mu.Lock()
	s.usage[user] = n
	s.mu.Unlock()
	return n
}

func (s *Store) Usage(user string) (int64, error) {
	r, err := s.root(user)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	return s.used(user, r), nil
}

func (s *Store) forget(user string) {
	s.mu.Lock()
	delete(s.usage, user)
	s.mu.Unlock()
}

func (s *Store) Put(user, p string, body io.Reader, match string) (Entry, error) {
	c, err := clean(p)
	if err != nil || c == "." {
		return Entry{}, ErrName
	}
	r, err := s.root(user)
	if err != nil {
		return Entry{}, err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	var old int64
	info, err := r.Lstat(c)
	if err == nil {
		if !info.Mode().IsRegular() {
			return Entry{}, ErrName
		}
		old = info.Size()
	}
	if match != "" && match != "*" && (err != nil || ETag(info) != match) {
		return Entry{}, ErrChanged
	}
	room := s.Quota - s.used(user, r) + old
	if room < 0 {
		room = 0
	}
	dir := path.Dir(c)
	if dir != "." {
		if err := r.MkdirAll(dir, 0o700); err != nil {
			return Entry{}, err
		}
	}
	tmp := path.Join(dir, partial+randomName())
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Entry{}, err
	}
	n, err := io.Copy(f, io.LimitReader(body, room+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > room {
		err = ErrQuota
	}
	if err != nil {
		r.Remove(tmp)
		return Entry{}, err
	}
	if err := r.Rename(tmp, c); err != nil {
		r.Remove(tmp)
		return Entry{}, err
	}
	s.mu.Lock()
	if u, ok := s.usage[user]; ok {
		s.usage[user] = u - old + n
	}
	s.mu.Unlock()
	info, err = r.Lstat(c)
	if err != nil {
		return Entry{}, err
	}
	return EntryOf(info), nil
}

func (s *Store) Mkdir(user, p string) error {
	c, err := clean(p)
	if err != nil || c == "." {
		return ErrName
	}
	r, err := s.root(user)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.MkdirAll(c, 0o700)
}

func (s *Store) Remove(user, p string) error {
	c, err := clean(p)
	if err != nil || c == "." {
		return ErrName
	}
	r, err := s.root(user)
	if err != nil {
		return err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	if _, err := r.Lstat(c); err != nil {
		return err
	}
	defer s.forget(user)
	return r.RemoveAll(c)
}

func (s *Store) Move(user, from, to string) error {
	a, err := clean(from)
	if err != nil || a == "." {
		return ErrName
	}
	b, err := clean(to)
	if err != nil || b == "." || b == a || strings.HasPrefix(b, a+"/") {
		return ErrName
	}
	r, err := s.root(user)
	if err != nil {
		return err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	if _, err := r.Lstat(b); err == nil {
		return fs.ErrExist
	}
	if d := path.Dir(b); d != "." {
		if err := r.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if err := r.Rename(a, b); err != nil {
		return err
	}
	moved := func(p string) (string, bool) {
		if p == a {
			return b, true
		}
		if rest, ok := strings.CutPrefix(p, a+"/"); ok {
			return b + "/" + rest, true
		}
		return p, true
	}
	s.editStars(r, moved)
	s.editLinks(user, moved)
	return nil
}
