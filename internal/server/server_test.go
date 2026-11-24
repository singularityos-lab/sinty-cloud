package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/singularityos-lab/sinty-cloud/internal/files"
	"github.com/singularityos-lab/sinty-identity/rp"
)

type fakeAuth struct{ weak bool }

func (fakeAuth) Begin(...string) rp.Request {
	return rp.Request{URL: "https://id.example/authorize?state=s1", State: "s1"}
}

func (f fakeAuth) Finish(_ context.Context, req rp.Request, answer url.Values) (rp.Claims, error) {
	if answer.Get("state") != req.State {
		return rp.Claims{}, rp.ErrState
	}
	if f.weak {
		return rp.Claims{}, rp.ErrWeak
	}
	return rp.Claims{Subject: answer.Get("who"), Level: 2}, nil
}

func (fakeAuth) Access(_ context.Context, bearer string) (rp.Claims, error) {
	if who, ok := strings.CutPrefix(bearer, "tok-"); ok {
		return rp.Claims{Subject: who, Level: 2}, nil
	}
	return rp.Claims{}, rp.ErrToken
}

func setup(t *testing.T, auth Auth) *httptest.Server {
	s := New(auth, files.New(t.TempDir(), 64), "", false)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func do(t *testing.T, c *http.Client, method, u, bearer string, body io.Reader, header bool) *http.Response {
	req, _ := http.NewRequest(method, u, body)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if header {
		req.Header.Set(rp.RequestHeader, "1")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestBrowserSession(t *testing.T) {
	srv := setup(t, fakeAuth{})
	c := client()
	if r := do(t, c, "GET", srv.URL+"/", "", nil, false); r.StatusCode != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/login") {
		t.Fatal("the app opened without a session")
	}
	if r := do(t, c, "GET", srv.URL+"/cb?state=s1&who=alice", "", nil, false); r.StatusCode != http.StatusBadRequest {
		t.Fatal("a callback without a started sign-in was accepted")
	}
	r := do(t, c, "GET", srv.URL+"/login", "", nil, false)
	if !strings.HasPrefix(r.Header.Get("Location"), "https://id.example/authorize") {
		t.Fatal("login did not go to the provider")
	}
	if r := do(t, c, "GET", srv.URL+"/cb?state=s1&who=alice", "", nil, false); r.StatusCode != http.StatusFound {
		t.Fatalf("callback %d", r.StatusCode)
	}
	if r := do(t, c, "GET", srv.URL+"/cb?state=s1&who=alice", "", nil, false); r.StatusCode != http.StatusBadRequest {
		t.Fatal("a callback was accepted twice")
	}
	if r := do(t, c, "GET", srv.URL+"/", "", nil, false); r.StatusCode != http.StatusOK || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("the app did not open after signing in")
	}
	if r := do(t, c, "PUT", srv.URL+"/api/v1/files/a.html", "", strings.NewReader("<script>alert(1)</script>"), false); r.StatusCode != http.StatusBadRequest {
		t.Fatal("a cross-site request without the request header was accepted")
	}
	if r := do(t, c, "PUT", srv.URL+"/api/v1/files/a.html", "", strings.NewReader("<script>alert(1)</script>"), true); r.StatusCode != http.StatusOK {
		t.Fatalf("upload %d", r.StatusCode)
	}
	r = do(t, c, "GET", srv.URL+"/api/v1/files/a.html", "", nil, true)
	if r.Header.Get("Content-Security-Policy") != "sandbox" || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") || r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("a download can run in the page: %v", r.Header)
	}
	if r := do(t, c, "POST", srv.URL+"/logout", "", nil, true); r.StatusCode != http.StatusNoContent {
		t.Fatal("logout")
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/me", "", nil, true); r.StatusCode != http.StatusUnauthorized {
		t.Fatal("the session outlived logout")
	}
}

func TestWeakSignIn(t *testing.T) {
	srv := setup(t, fakeAuth{weak: true})
	c := client()
	do(t, c, "GET", srv.URL+"/login", "", nil, false)
	if r := do(t, c, "GET", srv.URL+"/cb?state=s1&who=alice", "", nil, false); r.StatusCode != http.StatusForbidden {
		t.Fatal("a weak sign-in opened the cloud")
	}
}

func TestBearer(t *testing.T) {
	srv := setup(t, fakeAuth{})
	c := client()
	if r := do(t, c, "PUT", srv.URL+"/api/v1/files/docs/n.txt", "tok-alice", strings.NewReader("alice notes"), false); r.StatusCode != http.StatusOK {
		t.Fatalf("bearer upload %d", r.StatusCode)
	}
	r := do(t, c, "GET", srv.URL+"/api/v1/files/docs", "tok-bob", nil, false)
	var listing struct{ Entries []files.Entry }
	if r.StatusCode != http.StatusNotFound {
		json.NewDecoder(r.Body).Decode(&listing)
		t.Fatalf("bob saw alice's folder: %d %+v", r.StatusCode, listing)
	}
	r = do(t, c, "GET", srv.URL+"/api/v1/files/", "tok-alice", nil, false)
	json.NewDecoder(r.Body).Decode(&listing)
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "docs" {
		t.Fatalf("listing %+v", listing)
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/me", "bad", nil, false); r.StatusCode != http.StatusUnauthorized {
		t.Fatal("a bad token was accepted")
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/me", "tok-../etc", nil, false); r.StatusCode != http.StatusUnauthorized {
		t.Fatal("a crafted subject reached the store")
	}
	if r := do(t, c, "PUT", srv.URL+"/api/v1/files/big", "tok-alice", strings.NewReader(strings.Repeat("x", 80)), false); r.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("quota answer %d", r.StatusCode)
	}
	if r := do(t, c, "POST", srv.URL+"/api/v1/move", "tok-alice", strings.NewReader(`{"from":"docs/n.txt","to":"old/n.txt"}`), false); r.StatusCode != http.StatusNoContent {
		t.Fatalf("move %d", r.StatusCode)
	}
	if r := do(t, c, "DELETE", srv.URL+"/api/v1/files/old", "tok-alice", nil, false); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d", r.StatusCode)
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/files/old/n.txt", "tok-alice", nil, false); r.StatusCode != http.StatusNotFound {
		t.Fatal("a deleted file is still there")
	}
}

func entryOf(t *testing.T, r *http.Response) files.Entry {
	defer r.Body.Close()
	var e files.Entry
	if r.StatusCode != http.StatusOK || json.NewDecoder(r.Body).Decode(&e) != nil {
		t.Fatalf("expected an entry, got %d", r.StatusCode)
	}
	return e
}

func TestConditionalReplace(t *testing.T) {
	srv := setup(t, fakeAuth{})
	c := client()
	put := func(body, match string) *http.Response {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/files/notes.txt", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok-alice")
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if r := put("first", `"0-0"`); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("a conditional write created a missing file: %d", r.StatusCode)
	}
	first := entryOf(t, put("first", ""))
	if first.Name != "notes.txt" || first.Size != 5 || !strings.HasPrefix(first.ETag, `"`) {
		t.Fatalf("upload answer %+v", first)
	}
	info := entryOf(t, do(t, c, "GET", srv.URL+"/api/v1/info/notes.txt", "tok-alice", nil, false))
	if info.ETag != first.ETag || info.Size != 5 {
		t.Fatalf("info %+v, upload %+v", info, first)
	}
	r := do(t, c, "GET", srv.URL+"/api/v1/files/notes.txt", "tok-alice", nil, false)
	r.Body.Close()
	if r.Header.Get("ETag") != first.ETag {
		t.Fatalf("download tag %q, entry tag %q", r.Header.Get("ETag"), first.ETag)
	}
	second := entryOf(t, put("second!", first.ETag))
	if second.ETag == first.ETag || second.Size != 7 {
		t.Fatalf("replace answer %+v", second)
	}
	if r := put("third", first.ETag); r.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("a stale tag replaced newer content: %d", r.StatusCode)
	}
	if got := entryOf(t, do(t, c, "GET", srv.URL+"/api/v1/info/notes.txt", "tok-alice", nil, false)); got.ETag != second.ETag {
		t.Fatal("a refused write changed the file")
	}
	root := entryOf(t, do(t, c, "GET", srv.URL+"/api/v1/info/", "tok-alice", nil, false))
	if !root.Dir || root.Name != "" || root.ETag != "" {
		t.Fatalf("root info %+v", root)
	}
	if r := do(t, c, "GET", srv.URL+"/api/v1/info/missing", "tok-alice", nil, false); r.StatusCode != http.StatusNotFound {
		t.Fatalf("missing info %d", r.StatusCode)
	}
}
