//go:build js && wasm

// Package term is a websh shell as a desk pane.
//
// It used to be two hundred lines of line editor and stdin plumbing copied out
// of websh's cmd directory. websh now exports that as web.NewSession, so this
// is the adapter it always should have been: a Mount and a Close.
//
// Every pane opened here shares one filesystem. That is what lets a command in
// one window hand a file to another window without any message passing — the
// filesystem is the channel.
package term

import (
	"sync"
	"syscall/js"

	"github.com/0magnet/afero"
	"github.com/0magnet/websh/shell"
	"github.com/0magnet/websh/web"
)

var (
	fsOnce   sync.Once
	sharedFS afero.Fs
)

// FS is the filesystem every pane shares. It is seeded on first use.
func FS() afero.Fs {
	fsOnce.Do(func() {
		sharedFS = afero.NewMemMapFs()
		if err := shell.Seed(sharedFS); err != nil {
			js.Global().Get("console").Call("warn", "term: seed failed: "+err.Error())
		}
	})
	return sharedFS
}

// Pane is a terminal running a shell.
type Pane struct {
	greeting string
	host     string
	session  *web.Session
}

// New makes a terminal pane. An empty host is named for the desk.
func New(greeting, host string) *Pane {
	if host == "" {
		host = "desk"
	}
	return &Pane{greeting: greeting, host: host}
}

// Mount starts a shell on el.
func (p *Pane) Mount(el js.Value) error {
	s, err := web.NewSession(el, web.Options{
		FS:       FS(),
		Host:     p.host,
		Greeting: p.greeting,
	})
	if err != nil {
		return err
	}
	p.session = s
	return nil
}

// Close ends the session. The shared filesystem outlives it.
func (p *Pane) Close() {
	if p.session != nil {
		p.session.Close()
		p.session = nil
	}
}
