package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, s *Store, user, p, body string) {
	t.Helper()
	if _, err := s.Put(user, p, strings.NewReader(body), ""); err != nil {
		t.Fatal(p, err)
	}
}

func TestReservedNames(t *testing.T) {
	s := New(t.TempDir(), 1000)
	for _, p := range []string{".sinty-trash/x", "a/.sinty-meta/stars.json", ".sinty-anything"} {
		if _, err := s.Put("alice", p, strings.NewReader("x"), ""); !errors.Is(err, ErrName) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	put(t, s, "alice", "a.txt", "1")
	if _, err := s.Trash("alice", "a.txt"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List("alice", ""); len(list) != 0 {
		t.Fatalf("internal folders are listed: %+v", list)
	}
}

func TestTrash(t *testing.T) {
	s := New(t.TempDir(), 1000)
	put(t, s, "alice", "docs/a.txt", "hello")
	put(t, s, "alice", "docs/b.txt", "world")
	item, err := s.Trash("alice", "docs")
	if err != nil || !item.Dir || item.Size != 10 || item.From != "docs" {
		t.Fatalf("%+v %v", item, err)
	}
	if _, err := s.Stat("alice", "docs"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the folder is still there")
	}
	if used, _ := s.Usage("alice"); used != 10 {
		t.Fatalf("the trash does not count: %d", used)
	}
	put(t, s, "alice", "docs/c.txt", "new")
	p, e, err := s.Restore("alice", item.ID)
	if err != nil || p != "docs (2)" || !e.Dir {
		t.Fatalf("%s %+v %v", p, e, err)
	}
	if items, _ := s.TrashList("alice"); len(items) != 0 {
		t.Fatalf("%+v", items)
	}
	one, _ := s.Trash("alice", "docs/c.txt")
	two, _ := s.Trash("alice", "docs (2)/a.txt")
	if err := s.Purge("alice", one.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge("bob", two.ID); err == nil {
		t.Fatal("bob purged alice's item")
	}
	if err := s.Purge("alice", "../../x"); !errors.Is(err, ErrName) {
		t.Fatal(err)
	}
	items, _ := s.TrashList("alice")
	if len(items) != 1 || items[0].ID != two.ID {
		t.Fatalf("%+v", items)
	}
	meta := filepath.Join(s.Dir, "users", "alice", trashDir, two.ID+".json")
	data, _ := os.ReadFile(meta)
	old := strings.Replace(string(data), items[0].Deleted.Format(time.RFC3339Nano), time.Now().Add(-TrashKeep-time.Hour).UTC().Format(time.RFC3339Nano), 1)
	os.WriteFile(meta, []byte(old), 0o600)
	if items, _ := s.TrashList("alice"); len(items) != 0 {
		t.Fatalf("an old item was kept: %+v", items)
	}
	if used, _ := s.Usage("alice"); used != 5 {
		t.Fatalf("used %d", used)
	}
	s.Trash("alice", "docs (2)")
	if err := s.EmptyTrash("alice"); err != nil {
		t.Fatal(err)
	}
	if used, _ := s.Usage("alice"); used != 0 {
		t.Fatalf("used %d", used)
	}
}

func TestSearchAndRecent(t *testing.T) {
	s := New(t.TempDir(), 1000)
	put(t, s, "alice", "Trip/Lisbon.jpg", "1")
	time.Sleep(10 * time.Millisecond)
	put(t, s, "alice", "notes/lisbon plan.txt", "2")
	put(t, s, "alice", "gone/lisbon.txt", "3")
	s.Trash("alice", "gone")
	found, err := s.Search("alice", "LISBON", 10)
	if err != nil || len(found) != 2 {
		t.Fatalf("%+v %v", found, err)
	}
	if found, _ := s.Search("alice", "lisbon", 1); len(found) != 1 {
		t.Fatal("the limit is ignored")
	}
	recent, _ := s.Recent("alice", 10)
	if len(recent) != 2 || recent[0].Path != "notes/lisbon plan.txt" {
		t.Fatalf("%+v", recent)
	}
}

func TestStars(t *testing.T) {
	s := New(t.TempDir(), 1000)
	put(t, s, "alice", "a/b.txt", "1")
	if err := s.Star("alice", "a/b.txt", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Star("alice", "missing", true); err == nil {
		t.Fatal("starred a missing file")
	}
	s.Move("alice", "a", "c")
	stars, _ := s.Stars("alice")
	if len(stars) != 1 || stars[0].Path != "c/b.txt" || !stars[0].Starred {
		t.Fatalf("%+v", stars)
	}
	s.Trash("alice", "c")
	if stars, _ := s.Stars("alice"); len(stars) != 0 {
		t.Fatalf("%+v", stars)
	}
}

func TestLinks(t *testing.T) {
	s := New(t.TempDir(), 1000)
	put(t, s, "alice", "a/report.pdf", "pdf")
	s.Mkdir("alice", "folder")
	if _, err := s.Share("alice", "folder", 0); !errors.Is(err, ErrNotFile) {
		t.Fatal(err)
	}
	l, err := s.Share("alice", "a/report.pdf", 0)
	if err != nil || len(l.Token) != 32 {
		t.Fatalf("%+v %v", l, err)
	}
	s.Move("alice", "a", "b")
	got, info, err := s.Resolve(l.Token)
	if err != nil || got.Path != "b/report.pdf" || info.Size() != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.Unshare("bob", l.Token); err == nil {
		t.Fatal("bob removed alice's link")
	}
	short, _ := s.Share("alice", "b/report.pdf", time.Nanosecond)
	time.Sleep(time.Millisecond)
	if _, _, err := s.Resolve(short.Token); !errors.Is(err, ErrExpired) && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if list, _ := s.Links("alice"); len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	s.Trash("alice", "b/report.pdf")
	if _, _, err := s.Resolve(l.Token); err == nil {
		t.Fatal("a link to a trashed file still works")
	}
	if _, _, err := s.Resolve("../../users/alice/x"); err == nil {
		t.Fatal("a bad token resolved")
	}
}

func TestCopy(t *testing.T) {
	s := New(t.TempDir(), 14)
	put(t, s, "alice", "d/a.txt", "abcd")
	put(t, s, "alice", "d/e/b.txt", "ef")
	if _, _, err := s.Copy("alice", "d", "d/inside"); !errors.Is(err, ErrName) {
		t.Fatal("a folder was copied into itself", err)
	}
	p, e, err := s.Copy("alice", "d", "x")
	if err != nil || p != "x" || !e.Dir {
		t.Fatal(p, e, err)
	}
	f, err := s.Open("alice", "x/e/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := s.Copy("alice", "d", "y"); !errors.Is(err, ErrQuota) {
		t.Fatal("copy went past the quota", err)
	}
	p, _, err = s.Copy("alice", "d/e/b.txt", "d/e/b.txt.copy")
	if err != nil || p != "d/e/b.txt.copy" {
		t.Fatal(p, err)
	}
	s.Quota = 100
	if p, _, err := s.Copy("alice", "d/a.txt", "d/a.txt"); err != nil || p != "d/a (2).txt" {
		t.Fatal(p, err)
	}
}

func TestRestoreKeepsStarsAndLinks(t *testing.T) {
	s := New(t.TempDir(), 1000)
	put(t, s, "alice", "docs/a.txt", "hello")
	put(t, s, "alice", "docs/b.txt", "world")
	put(t, s, "alice", "other.txt", "x")
	s.Star("alice", "docs", true)
	s.Star("alice", "docs/a.txt", true)
	s.Star("alice", "other.txt", true)
	l, err := s.Share("alice", "docs/b.txt", 0)
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Trash("alice", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Resolve(l.Token); err == nil {
		t.Fatal("a link to a file in the trash still works")
	}
	if set, _ := s.StarSet("alice"); len(set) != 1 || !set["other.txt"] {
		t.Fatalf("stars while in the trash: %v", set)
	}
	list, _ := s.TrashList("alice")
	if len(list) != 1 || list[0].ID != item.ID {
		t.Fatalf("trash: %+v", list)
	}
	put(t, s, "alice", "docs/new.txt", "taken")
	to, _, err := s.Restore("alice", item.ID)
	if err != nil || to != "docs (2)" {
		t.Fatalf("restore: %q %v", to, err)
	}
	if set, _ := s.StarSet("alice"); len(set) != 3 || !set["docs (2)"] || !set["docs (2)/a.txt"] {
		t.Fatalf("stars after restoring: %v", set)
	}
	back, _, err := s.Resolve(l.Token)
	if err != nil || back.Path != "docs (2)/b.txt" {
		t.Fatalf("the link did not come back: %+v %v", back, err)
	}
}
