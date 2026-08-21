//go:build !js

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"

	"github.com/0magnet/desk/panes/hostagent"
	"github.com/0magnet/desk/panes/hostproto"
)

// The host agent is wired up here rather than in hostagent because deciding
// whether to expose the machine is the command's business, not the library's.
// A package that mounts itself is a package that gets mounted by accident.

// hostConfig is what the page is told about the agent. It is injected into
// index.html as a global, which is the only delivery that has no race: a page
// that has to fetch its token can open a window before the answer arrives, and
// then the first shell of every session fails once.
type hostConfig struct {
	Token string `json:"token"`
	Path  string `json:"path,omitempty"` // the pty endpoint; empty when --shell is off
	FS    bool   `json:"fs,omitempty"`   // the filesystem endpoints are served
}

// injectHostConfig serves index.html with the agent's config prepended.
//
// Rewriting on the way out rather than committing a placeholder into docs/
// keeps the built page a static artifact that still works when served by
// anything else — with no agent, the global is simply absent and the pane says
// so, which is exactly what should happen on GitHub Pages.
func injectHostConfig(assets fs.FS, next http.Handler, cfg hostConfig) (http.Handler, error) {
	blob, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding the host config: %w", err)
	}
	// json.Marshal is what makes this safe to interpolate: the token is hex
	// from crypto/rand and the path is a constant, but building script from
	// string concatenation is a habit worth not having.
	tag := []byte("<script>window.__deskHost=" + string(blob) + ";</script>\n")

	// EVERY page, not just the top one. docs/ carries two builds — TinyGo at
	// the root and standard Go under go/ — and they exist precisely so that
	// one can be checked against the other when something misbehaves. A token
	// that reached only the first would make the host shell look like the
	// thing TinyGo had miscompiled.
	pages := map[string][]byte{}
	err = fs.WalkDir(assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) != "index.html" {
			return err
		}
		body, err := fs.ReadFile(assets, p)
		if err != nil {
			return err
		}
		i := bytes.Index(body, []byte("</head>"))
		if i < 0 {
			return nil // not a page shaped like ours; serve it untouched
		}
		out := make([]byte, 0, len(body)+len(tag))
		out = append(out, body[:i]...)
		out = append(out, tag...)
		out = append(out, body[i:]...)

		dir := "/" + path.Dir(p) + "/"
		if p == "index.html" {
			dir = "/"
		}
		pages[dir] = out
		pages[dir+"index.html"] = out
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the embedded pages: %w", err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no embedded page had a </head> to inject into")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body) //nolint:errcheck // the client hung up; there is no second channel to say so on
	}), nil
}

// servedOrigins is the set of Origin values a browser might send for a page
// this listener served.
//
// Both spellings are needed because they are different origins to the browser
// and only one of them is what the address bar ends up saying: the agent may
// print 127.0.0.1 while the person types localhost, or the reverse, and a
// mismatch would refuse the connection for a reason that looks like a bug.
func servedOrigins(ln net.Listener) []string {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return []string{"http://" + ln.Addr().String()}
	}
	port := addr.Port
	return []string{
		fmt.Sprintf("http://127.0.0.1:%d", port),
		fmt.Sprintf("http://localhost:%d", port),
		fmt.Sprintf("http://[::1]:%d", port),
	}
}

// mountHostAgent adds whichever endpoints were asked for and returns the config
// for the page. One token covers both: they are the same grant of access to the
// same machine, and two would only suggest otherwise.
func mountHostAgent(mux *http.ServeMux, ln net.Listener, opt hostOptions) (hostConfig, error) {
	token, err := hostagent.NewToken()
	if err != nil {
		return hostConfig{}, err
	}
	agent := hostagent.Config{
		Token:   token,
		Origins: servedOrigins(ln),
		Session: hostagent.SessionConfig{Shell: opt.shell},
	}
	cfg := hostConfig{Token: token}
	if opt.wantShell {
		mux.Handle(hostproto.Path, agent.Handler())
		cfg.Path = hostproto.Path
	}
	if opt.wantFS {
		mux.Handle(hostproto.FSPath, agent.FSHandler(hostagent.FSConfig{Root: opt.fsRoot}))
		cfg.FS = true
	}
	return cfg, nil
}

// hostOptions is what the flags asked for.
type hostOptions struct {
	wantShell bool
	wantFS    bool
	shell     string
	fsRoot    string
}

// warnAboutHostAccess says what was just turned on.
//
// Loudly, and every time, because the whole risk of this feature is someone
// leaving it running after they stopped thinking about it.
func warnAboutHostAccess(opt hostOptions) {
	if opt.wantShell {
		fmt.Printf("desk: --shell is ON: this page can run commands on this machine as you.\n")
	}
	if opt.wantFS {
		scope := "your whole filesystem"
		if opt.fsRoot != "" {
			scope = opt.fsRoot
		}
		fmt.Printf("desk: --fs is ON: this page can read and write %s.\n", scope)
	}
	fmt.Printf("desk:   guarded by a per-run token and an Origin check; stop the server to revoke both.\n")
}
