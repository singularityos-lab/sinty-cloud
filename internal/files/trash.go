package files

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const TrashKeep = 30 * 24 * time.Hour

var itemID = regexp.MustCompile(`^[0-9a-f]{24}$`)

type TrashItem struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	From    string    `json:"from"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	Deleted time.Time `json:"deleted"`
}

type trashRecord struct {
	TrashItem
	Stars []string `json:"stars,omitempty"`
	Links []Link   `json:"links,omitempty"`
}

func under(q, c string) (string, bool) {
	if q == c {
		return "", true
	}
	if rest, ok := strings.CutPrefix(q, c+"/"); ok {
		return "/" + rest, true
	}
	return "", false
}

func sizeOf(r *os.Root, p string) int64 {
	var n int64
	fs.WalkDir(r.FS(), p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func writeJSON(r *os.Root, p string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path.Join(path.Dir(p), partial+randomName())
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = r.Rename(tmp, p)
	}
	if err != nil {
		r.Remove(tmp)
	}
	return err
}

func freeName(r *os.Root, p string) string {
	if _, err := r.Lstat(p); err != nil {
		return p
	}
	dir, name := path.Split(p)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	for i := 2; ; i++ {
		c := dir + stem + " (" + strconv.Itoa(i) + ")" + ext
		if _, err := r.Lstat(c); err != nil {
			return c
		}
	}
}

func (s *Store) Trash(user, p string) (TrashItem, error) {
	c, err := clean(p)
	if err != nil || c == "." {
		return TrashItem{}, ErrName
	}
	r, err := s.root(user)
	if err != nil {
		return TrashItem{}, err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	info, err := r.Lstat(c)
	if err != nil {
		return TrashItem{}, err
	}
	if err := r.MkdirAll(trashDir, 0o700); err != nil {
		return TrashItem{}, err
	}
	item := TrashItem{ID: randomName(), Name: path.Base(c), From: c, Dir: info.IsDir(), Size: info.Size(), Deleted: time.Now().UTC()}
	if item.Dir {
		item.Size = sizeOf(r, c)
	}
	rec := trashRecord{TrashItem: item}
	for _, q := range loadStars(r) {
		if rel, ok := under(q, c); ok {
			rec.Stars = append(rec.Stars, rel)
		}
	}
	links, _ := s.Links(user)
	for _, l := range links {
		if rel, ok := under(l.Path, c); ok {
			l.Path = rel
			rec.Links = append(rec.Links, l)
		}
	}
	meta := path.Join(trashDir, item.ID+".json")
	if err := writeJSON(r, meta, rec); err != nil {
		return TrashItem{}, err
	}
	if err := r.Rename(c, path.Join(trashDir, item.ID)); err != nil {
		r.Remove(meta)
		return TrashItem{}, err
	}
	gone := func(q string) (string, bool) {
		return q, q != c && !strings.HasPrefix(q, c+"/")
	}
	s.editStars(r, gone)
	s.editLinks(user, gone)
	return item, nil
}

func (s *Store) trashItems(r *os.Root) []TrashItem {
	entries, err := fs.ReadDir(r.FS(), trashDir)
	if err != nil {
		return nil
	}
	out := []TrashItem{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !itemID.MatchString(id) {
			continue
		}
		data, err := fs.ReadFile(r.FS(), path.Join(trashDir, e.Name()))
		if err != nil {
			continue
		}
		var rec trashRecord
		if json.Unmarshal(data, &rec) != nil || rec.ID != id {
			continue
		}
		out = append(out, rec.TrashItem)
	}
	return out
}

func (s *Store) TrashList(user string) ([]TrashItem, error) {
	r, err := s.root(user)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	out := []TrashItem{}
	purged := false
	for _, it := range s.trashItems(r) {
		if time.Since(it.Deleted) > TrashKeep {
			r.RemoveAll(path.Join(trashDir, it.ID))
			r.Remove(path.Join(trashDir, it.ID+".json"))
			purged = true
			continue
		}
		out = append(out, it)
	}
	if purged {
		s.forget(user)
	}
	slices.SortFunc(out, func(a, b TrashItem) int { return b.Deleted.Compare(a.Deleted) })
	return out, nil
}

func (s *Store) Restore(user, id string) (string, Entry, error) {
	if !itemID.MatchString(id) {
		return "", Entry{}, ErrName
	}
	r, err := s.root(user)
	if err != nil {
		return "", Entry{}, err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	data, err := fs.ReadFile(r.FS(), path.Join(trashDir, id+".json"))
	if err != nil {
		return "", Entry{}, err
	}
	var it trashRecord
	if err := json.Unmarshal(data, &it); err != nil {
		return "", Entry{}, err
	}
	to, err := clean(it.From)
	if err != nil || to == "." {
		to = it.Name
		if to, err = clean(to); err != nil || to == "." {
			return "", Entry{}, ErrName
		}
	}
	if d := path.Dir(to); d != "." {
		if err := r.MkdirAll(d, 0o700); err != nil {
			return "", Entry{}, err
		}
	}
	to = freeName(r, to)
	if err := r.Rename(path.Join(trashDir, id), to); err != nil {
		return "", Entry{}, err
	}
	r.Remove(path.Join(trashDir, id+".json"))
	if len(it.Stars) > 0 {
		list := loadStars(r)
		for _, rel := range it.Stars {
			list = append(list, to+rel)
		}
		saveStars(r, list)
	}
	s.relink(it.Links, to)
	info, err := r.Lstat(to)
	if err != nil {
		return "", Entry{}, err
	}
	return to, EntryOf(info), nil
}

func (s *Store) Purge(user, id string) error {
	if !itemID.MatchString(id) {
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
	if _, err := r.Lstat(path.Join(trashDir, id+".json")); err != nil {
		return err
	}
	defer s.forget(user)
	if err := r.RemoveAll(path.Join(trashDir, id)); err != nil {
		return err
	}
	return r.Remove(path.Join(trashDir, id+".json"))
}

func (s *Store) EmptyTrash(user string) error {
	r, err := s.root(user)
	if err != nil {
		return err
	}
	defer r.Close()
	m := s.lock(user)
	m.Lock()
	defer m.Unlock()
	defer s.forget(user)
	return r.RemoveAll(trashDir)
}
