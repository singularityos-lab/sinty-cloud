package files

import (
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

func copyFile(r *os.Root, from, to string) error {
	src, err := r.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := path.Join(path.Dir(to), partial+randomName())
	dst, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = r.Rename(tmp, to)
	}
	if err != nil {
		r.Remove(tmp)
	}
	return err
}

func (s *Store) Copy(user, from, to string) (string, Entry, error) {
	a, err := clean(from)
	if err != nil || a == "." {
		return "", Entry{}, ErrName
	}
	b, err := clean(to)
	if err != nil || b == "." || strings.HasPrefix(b, a+"/") {
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
	info, err := r.Lstat(a)
	if err != nil {
		return "", Entry{}, err
	}
	need := info.Size()
	if info.IsDir() {
		need = sizeOf(r, a)
	}
	if s.used(user, r)+need > s.Quota {
		return "", Entry{}, ErrQuota
	}
	if d := path.Dir(b); d != "." {
		if err := r.MkdirAll(d, 0o700); err != nil {
			return "", Entry{}, err
		}
	}
	b = freeName(r, b)
	defer s.forget(user)
	if !info.IsDir() {
		if err := copyFile(r, a, b); err != nil {
			return "", Entry{}, err
		}
	} else {
		err = fs.WalkDir(r.FS(), a, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.HasPrefix(d.Name(), reserved) || d.Type()&fs.ModeSymlink != 0 {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			target := b + strings.TrimPrefix(p, a)
			if d.IsDir() {
				return r.MkdirAll(target, 0o700)
			}
			if d.Type().IsRegular() {
				return copyFile(r, p, target)
			}
			return nil
		})
		if err != nil {
			r.RemoveAll(b)
			return "", Entry{}, err
		}
	}
	info, err = r.Lstat(b)
	if err != nil {
		return "", Entry{}, err
	}
	return b, EntryOf(info), nil
}
