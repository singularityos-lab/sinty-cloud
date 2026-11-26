package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/singularityos-lab/sinty-cloud/internal/files"
)

func big(t *testing.T) *httptest.Server {
	s := New(fakeAuth{}, files.New(t.TempDir(), 1<<20), "", false)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func read(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("status %d %s", r.StatusCode, b)
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

var png = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"

func TestPreview(t *testing.T) {
	srv := big(t)
	c := client()
	for name, body := range map[string]string{"p.png": png, "t.txt": "plain words", "x.svg": `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, "h.png": "<!doctype html><script>alert(1)</script>"} {
		if r := do(t, c, "PUT", srv.URL+"/api/v1/files/"+name, "tok-alice", strings.NewReader(body), false); r.StatusCode != http.StatusOK {
			t.Fatalf("upload %s %d", name, r.StatusCode)
		}
	}
	r := do(t, c, "GET", srv.URL+"/api/v1/preview/p.png", "tok-alice", nil, false)
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "image/png" || r.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("png preview %d %v", r.StatusCode, r.Header)
	}
	r = do(t, c, "GET", srv.URL+"/api/v1/preview/t.txt", "tok-alice", nil, false)
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("text preview %d %v", r.StatusCode, r.Header)
	}
	r = do(t, c, "GET", srv.URL+"/api/v1/preview/x.svg", "tok-alice", nil, false)
	if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "text/plain; charset=utf-8" || r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("an svg preview is not plain text: %d %v", r.StatusCode, r.Header)
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/preview/h.png", "tok-alice", nil, false); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("html was previewed: %d %v", r.StatusCode, r.Header)
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/preview/p.png", "tok-bob", nil, false); r.StatusCode != http.StatusNotFound {
		t.Fatal("bob previewed alice's file")
	}
}

func TestPublicLink(t *testing.T) {
	srv := big(t)
	c := client()
	do(t, c, "PUT", srv.URL+"/api/v1/files/docs/report.pdf", "tok-alice", strings.NewReader("%PDF-1.4 report"), false)
	if r := do(t, c, "POST", srv.URL+"/api/v1/links", "tok-alice", strings.NewReader(`{"path":"docs","days":7}`), false); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a folder was shared: %d", r.StatusCode)
	}
	if r := do(t, c, "POST", srv.URL+"/api/v1/links", "tok-alice", strings.NewReader(`{"path":"docs/report.pdf","days":9999}`), false); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a link without a sane expiry: %d", r.StatusCode)
	}
	var l files.Link
	read(t, do(t, c, "POST", srv.URL+"/api/v1/links", "tok-alice", strings.NewReader(`{"path":"docs/report.pdf","days":7}`), false), &l)
	if l.Expires == nil || l.Path != "docs/report.pdf" {
		t.Fatalf("%+v", l)
	}
	r := do(t, client(), "GET", srv.URL+"/s/"+l.Token, "", nil, false)
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusOK || string(body) != "%PDF-1.4 report" || r.Header.Get("Content-Security-Policy") != "sandbox" || !strings.Contains(r.Header.Get("Content-Disposition"), "report.pdf") {
		t.Fatalf("public download %d %q %v", r.StatusCode, body, r.Header)
	}
	var listing struct{ Entries []files.Entry }
	read(t, do(t, c, "GET", srv.URL+"/api/v1/files/docs", "tok-alice", nil, false), &listing)
	if len(listing.Entries) != 1 || !listing.Entries[0].Shared {
		t.Fatalf("the listing does not mark the shared file: %+v", listing)
	}
	if r := do(t, c, "DELETE", srv.URL+"/api/v1/links/"+l.Token, "tok-bob", nil, false); r.StatusCode != http.StatusNotFound {
		t.Fatal("bob removed alice's link")
	}
	if r := do(t, c, "DELETE", srv.URL+"/api/v1/links/"+l.Token, "tok-alice", nil, false); r.StatusCode != http.StatusNoContent {
		t.Fatal("alice could not remove her link")
	}
	if r := do(t, client(), "GET", srv.URL+"/s/"+l.Token, "", nil, false); r.StatusCode != http.StatusNotFound {
		t.Fatal("a removed link still downloads")
	}
}

func TestTrashStarsSearch(t *testing.T) {
	srv := big(t)
	c := client()
	do(t, c, "PUT", srv.URL+"/api/v1/files/Trip/Lisbon.txt", "tok-alice", strings.NewReader("tram"), false)
	if r := do(t, c, "PUT", srv.URL+"/api/v1/stars/Trip/Lisbon.txt", "tok-alice", nil, false); r.StatusCode != http.StatusNoContent {
		t.Fatalf("star %d", r.StatusCode)
	}
	var found struct{ Results []files.Found }
	read(t, do(t, c, "GET", srv.URL+"/api/v1/search?q=lisb", "tok-alice", nil, false), &found)
	if len(found.Results) != 1 || found.Results[0].Path != "Trip/Lisbon.txt" || !found.Results[0].Starred {
		t.Fatalf("search %+v", found)
	}
	read(t, do(t, c, "GET", srv.URL+"/api/v1/search?q=lisb", "tok-bob", nil, false), &found)
	if len(found.Results) != 0 {
		t.Fatal("bob found alice's file")
	}
	var item files.TrashItem
	read(t, do(t, c, "POST", srv.URL+"/api/v1/trash/Trip", "tok-alice", nil, false), &item)
	var trash struct {
		Items    []files.TrashItem
		KeepDays int `json:"keep_days"`
	}
	read(t, do(t, c, "GET", srv.URL+"/api/v1/trash", "tok-alice", nil, false), &trash)
	if len(trash.Items) != 1 || trash.KeepDays != 30 {
		t.Fatalf("trash %+v", trash)
	}
	var me struct{ Trash int64 }
	read(t, do(t, c, "GET", srv.URL+"/api/v1/me", "tok-alice", nil, false), &me)
	if me.Trash != 4 {
		t.Fatalf("me %+v", me)
	}
	var back files.Found
	read(t, do(t, c, "POST", srv.URL+"/api/v1/restore/"+item.ID, "tok-alice", nil, false), &back)
	if back.Path != "Trip" || !back.Dir {
		t.Fatalf("restore %+v", back)
	}
	read(t, do(t, c, "GET", srv.URL+"/api/v1/stars", "tok-alice", nil, false), &found)
	if len(found.Results) != 1 || found.Results[0].Path != "Trip/Lisbon.txt" {
		t.Fatalf("the star did not come back with the folder: %+v", found.Results)
	}
	var copied files.Found
	read(t, do(t, c, "POST", srv.URL+"/api/v1/copy", "tok-alice", strings.NewReader(`{"from":"Trip","to":"Trip"}`), false), &copied)
	if copied.Path != "Trip (2)" || !copied.Dir {
		t.Fatalf("copy %+v", copied)
	}
}
