package files

import (
	"io/fs"
	"os"
	"slices"
	"strings"
)

type Found struct {
	Path string `json:"path"`
	Entry
}

func walkVisible(r *os.Root, fn func(p string, info fs.FileInfo) bool) {
	fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), reserved) || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if !fn(p, info) {
			return fs.SkipAll
		}
		return nil
	})
}

func (s *Store) Search(user, query string, limit int) ([]Found, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	out := []Found{}
	if q == "" {
		return out, nil
	}
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	walkVisible(r, func(p string, info fs.FileInfo) bool {
		if strings.Contains(strings.ToLower(info.Name()), q) {
			out = append(out, Found{Path: p, Entry: EntryOf(info)})
		}
		return len(out) < limit
	})
	return out, nil
}

func (s *Store) Recent(user string, limit int) ([]Found, error) {
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := []Found{}
	walkVisible(r, func(p string, info fs.FileInfo) bool {
		if info.Mode().IsRegular() {
			out = append(out, Found{Path: p, Entry: EntryOf(info)})
		}
		return true
	})
	slices.SortFunc(out, func(a, b Found) int { return b.Modified.Compare(a.Modified) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
