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
		req.Header.Set(requestHeader, "1")
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
	if r := do(t, c, "GET", srv.URL+"/", "", nil, false); r.StatusCode != http.StatusFound || r.Header.Get("Location") != "/login" {
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
