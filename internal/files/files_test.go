package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpace(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, 100)
	if _, err := s.Put("alice", "docs/a.txt", strings.NewReader("hello"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("alice", "../../bob/x", strings.NewReader("x"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "users", "alice", "bob", "x")); err != nil {
		t.Fatal("a path with .. left the user's space")
	}
	if _, err := os.Stat(filepath.Join(dir, "users", "bob")); err == nil {
		t.Fatal("alice wrote into bob's space")
	}
	os.WriteFile(filepath.Join(dir, "secret"), []byte("root only"), 0o600)
	os.Symlink(filepath.Join(dir, "secret"), filepath.Join(dir, "users", "alice", "link"))
	if f, err := s.Open("alice", "link"); err == nil {
		f.Close()
		t.Fatal("a symlink led out of the space")
	}
	entries, err := s.List("alice", "/")
	if err != nil || len(entries) != 2 || entries[0].Name != "bob" || !entries[0].Dir || entries[1].Name != "docs" {
		t.Fatalf("list: %v %+v", err, entries)
	}
	f, err := s.Open("alice", "docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" {
		t.Fatalf("content %q", b)
	}
	if _, err := s.Put("alice", "big", strings.NewReader(strings.Repeat("x", 95)), ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("quota not enforced: %v", err)
	}
	if u, _ := s.Usage("alice"); u != 6 {
		t.Fatalf("a refused upload changed the usage: %d", u)
	}
	if _, err := s.Put("alice", "docs/a.txt", strings.NewReader(strings.Repeat("y", 99)), ""); err != nil {
		t.Fatalf("replacing a file counts its old size as free: %v", err)
	}
	if u, _ := s.Usage("alice"); u != 100 {
		t.Fatalf("usage after replace: %d", u)
	}
	if _, err := s.Put("alice", ".sinty-upload-x", strings.NewReader("x"), ""); !errors.Is(err, ErrName) {
		t.Fatal("an upload could take a partial file name")
	}
	if err := s.Move("alice", "docs", "docs/inner"); !errors.Is(err, ErrName) {
		t.Fatal("a folder moved into itself")
	}
	if err := s.Move("alice", "docs/a.txt", "archive/a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("alice", "archive"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Usage("alice"); u != 1 {
		t.Fatalf("usage after remove: %d", u)
	}
	if err := s.Remove("alice", "/"); !errors.Is(err, ErrName) {
		t.Fatal("the whole space could be removed")
	}
	if _, err := s.List("../alice", "."); !errors.Is(err, ErrUser) {
		t.Fatal("a crafted user name was accepted")
	}
	info, _ := os.Stat(filepath.Join(dir, "users", "alice"))
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("space mode %v", info.Mode())
	}
}
