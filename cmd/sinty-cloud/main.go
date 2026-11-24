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
	auth, err := rp.Connect(ctx, cfg, func(f string, a ...any) { fmt.Fprintf(os.Stderr, "sinty-cloud: "+f+"\n", a...) })
	if err != nil {
		return err
	}
	s := server.New(auth, files.New(*data, *quota), *ui, strings.HasPrefix(*public, "https://"))
	return rp.Serve(ctx, *listen, s.Handler(), *cert, *key)
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
	claims, sess, err := rp.CommandLineLogin(ctx, "sinty-cloud", *srv, rp.Config{Issuer: *issuer, ClientID: *client})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in as %s until %s\n", claims.Name, sess.Expires.Local().Format(time.Kitchen))
	return nil
}

func load() (rp.Session, error) {
	sess, err := rp.LoadSession("sinty-cloud")
	if err != nil {
		return sess, exitError{3, "not signed in: run sinty-cloud login"}
	}
	return sess, nil
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

func call(c rp.Session, method, path string, body io.Reader) (*http.Response, error) {
	resp, err := c.Do(context.Background(), method, path, body)
	var api *rp.APIError
	switch {
	case errors.Is(err, rp.ErrSignedOut):
		return nil, exitError{3, "not signed in: run sinty-cloud login"}
	case errors.Is(err, rp.ErrNotFound):
		return nil, exitError{4, "not found"}
	case errors.As(err, &api) && api.Status == http.StatusInsufficientStorage:
		return nil, exitError{5, "the space is full"}
	case err != nil:
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return resp, nil
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
