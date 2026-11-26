package files

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"slices"
)

var starsFile = path.Join(metaDir, "stars.json")

func loadStars(r *os.Root) []string {
	data, err := fs.ReadFile(r.FS(), starsFile)
	if err != nil {
		return nil
	}
	var list []string
	if json.Unmarshal(data, &list) != nil {
		return nil
	}
	return list
}

func saveStars(r *os.Root, list []string) error {
	if err := r.MkdirAll(metaDir, 0o700); err != nil {
		return err
	}
	if list == nil {
		list = []string{}
	}
	return writeJSON(r, starsFile, list)
}

func (s *Store) editStars(r *os.Root, edit func(string) (string, bool)) {
	list := loadStars(r)
	if len(list) == 0 {
		return
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		if q, keep := edit(p); keep {
			out = append(out, q)
		}
	}
	if !slices.Equal(out, list) {
		saveStars(r, out)
	}
}

func (s *Store) StarSet(user string) (map[string]bool, error) {
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	set := map[string]bool{}
	for _, p := range loadStars(r) {
		set[p] = true
	}
	return set, nil
}

func (s *Store) Stars(user string) ([]Found, error) {
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := []Found{}
	for _, p := range loadStars(r) {
		if info, err := r.Lstat(p); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			e := EntryOf(info)
			e.Starred = true
			out = append(out, Found{Path: p, Entry: e})
		}
	}
	return out, nil
}

func (s *Store) Star(user, p string, on bool) error {
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
	list := slices.DeleteFunc(loadStars(r), func(q string) bool { return q == c })
	if on {
		list = append(list, c)
	}
	return saveStars(r, list)
}
