package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/singularityos-lab/sinty-cloud/internal/files"
	"github.com/singularityos-lab/sinty-cloud/internal/server"
	"github.com/singularityos-lab/sinty-identity/rp"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  sinty-cloud serve -issuer URL -client ID -secret-file FILE -public URL [-cli-client ID] [-data DIR] [-quota BYTES] [-listen ADDR] [-ui DIR] [-cert FILE -key FILE]
  sinty-cloud login -server URL -issuer URL [-client ID]
  sinty-cloud ls [PATH]
  sinty-cloud get REMOTE [LOCAL]
  sinty-cloud put LOCAL [REMOTE]
  sinty-cloud mkdir PATH
  sinty-cloud rm PATH
  sinty-cloud usage
Exit status: 0 done, 1 failed, 2 wrong usage, 3 signed out, 4 not found, 5 space full.
`)
	os.Exit(2)
}

type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "serve":
		err = serve(args)
	case "login":
		err = login(args)
	case "ls", "get", "put", "mkdir", "rm", "usage":
		err = remote(os.Args[1], args)
	default:
		usage()
	}
	var ee exitError
	if errors.As(err, &ee) {
		fmt.Fprintln(os.Stderr, "sinty-cloud:", ee.msg)
		os.Exit(ee.code)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sinty-cloud:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	issuer := fs.String("issuer", "", "identity provider")
	client := fs.String("client", "cloud", "client id at the identity provider")
	cliClient := fs.String("cli-client", "", "client id of the command line, whose tokens are accepted too")
	secretFile := fs.String("secret-file", "", "file holding the client secret")
	public := fs.String("public", "", "public address of this service")
	data := fs.String("data", "/var/lib/sinty-cloud", "storage directory")
	quota := fs.Int64("quota", 10<<30, "bytes per user")
	minLevel := fs.Int("min-level", 2, "weakest sign-in accepted")
	listen := fs.String("listen", ":8444", "address to listen on")
	ui := fs.String("ui", "/usr/share/sintyui", "sintyui directory")
	cert := fs.String("cert", "", "TLS certificate")
	key := fs.String("key", "", "TLS key")
	fs.Parse(args)
	if *issuer == "" || *public == "" {
		return exitError{2, "-issuer and -public are required"}
	}
	secret := ""
	if *secretFile != "" {
		raw, err := os.ReadFile(*secretFile)
		if err != nil {
			return err
		}
		secret = strings.TrimSpace(string(raw))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg := rp.Config{Issuer: *issuer, ClientID: *client, ClientSecret: secret, RedirectURL: strings.TrimRight(*public, "/") + "/cb", MinLevel: *minLevel}
	if *cliClient != "" {
		cfg.Audiences = []string{*cliClient}
	}
	var auth *rp.RP
	var err error
	for {
		auth, err = rp.New(ctx, cfg)
		if err == nil || ctx.Err() != nil {
			break
		}
		fmt.Fprintln(os.Stderr, "sinty-cloud: waiting for the identity provider:", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	if err != nil {
		return err
	}
	s := server.New(auth, files.New(*data, *quota), *ui, strings.HasPrefix(*public, "https://"))
	srv := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	if *cert != "" {
		err = srv.ListenAndServeTLS(*cert, *key)
	} else {
		err = srv.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type credentials struct {
	Server  string    `json:"server"`
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

func credentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sinty-cloud", "credentials.json"), nil
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	srv := fs.String("server", "", "cloud address")
	issuer := fs.String("issuer", "", "identity provider")
	client := fs.String("client", "cloud-cli", "client id of the command line")
	fs.Parse(args)
	if *srv == "" || *issuer == "" {
		return exitError{2, "-server and -issuer are required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	claims, tok, err := rp.LoopbackLogin(ctx, rp.Config{Issuer: *issuer, ClientID: *client}, func(u string) error {
		fmt.Fprintln(os.Stderr, "Sign in at:", u)
		if opener := os.Getenv("SINTY_CLOUD_BROWSER"); opener != "" {
			return exec.Command(opener, u).Start()
		}
		exec.Command("xdg-open", u).Start()
		return nil
	})
	if err != nil {
		return err
	}
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(credentials{Server: strings.TrimRight(*srv, "/"), Token: tok.Access, Expires: tok.Expires})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in as %s until %s\n", claims.Name, tok.Expires.Local().Format(time.Kitchen))
	return nil
}

func load() (credentials, error) {
	var c credentials
	path, err := credentialsPath()
	if err != nil {
		return c, err
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &c) != nil || time.Now().After(c.Expires) {
		return c, exitError{3, "not signed in: run sinty-cloud login"}
	}
	return c, nil
}

func escape(p string) string {
	var parts []string
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" {
			parts = append(parts, url.PathEscape(s))
		}
	}
	return strings.Join(parts, "/")
}

func call(c credentials, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.Server+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return resp, nil
	case http.StatusUnauthorized:
		resp.Body.Close()
		return nil, exitError{3, "not signed in: run sinty-cloud login"}
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, exitError{4, "not found"}
	case http.StatusInsufficientStorage:
		resp.Body.Close()
		return nil, exitError{5, "the space is full"}
	}
	var e struct{ Error string }
	json.NewDecoder(resp.Body).Decode(&e)
	resp.Body.Close()
	return nil, fmt.Errorf("%s %s: %s", method, path, e.Error)
}

func remote(verb string, args []string) error {
	c, err := load()
	if err != nil {
		return err
	}
	arg := func(i int, def string) string {
		if len(args) > i {
			return args[i]
		}
		return def
	}
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	switch verb {
	case "ls":
		resp, err := call(c, http.MethodGet, "/api/v1/files/"+escape(arg(0, "")), nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		var v any
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			return exitError{1, "that is a file, not a folder"}
		}
		return out.Encode(v)
	case "usage":
		resp, err := call(c, http.MethodGet, "/api/v1/me", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	case "get":
		if len(args) < 1 {
			usage()
		}
		resp, err := call(c, http.MethodGet, "/api/v1/files/"+escape(args[0]), nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		dest := arg(1, filepath.Base(args[0]))
		tmp := dest + ".part"
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, dest)
	case "put":
		if len(args) < 1 {
			usage()
		}
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		resp, err := call(c, http.MethodPut, "/api/v1/files/"+escape(arg(1, filepath.Base(args[0]))), f)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	case "mkdir":
		if len(args) < 1 {
			usage()
		}
		resp, err := call(c, http.MethodPost, "/api/v1/folders/"+escape(args[0]), nil)
		if err == nil {
			resp.Body.Close()
		}
		return err
	case "rm":
		if len(args) < 1 {
			usage()
		}
		resp, err := call(c, http.MethodDelete, "/api/v1/files/"+escape(args[0]), nil)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	usage()
	return nil
}
