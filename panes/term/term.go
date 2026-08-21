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

// SetFS replaces the shared filesystem, before anything has asked for one.
//
// It exists so a desk can be given the HOST's filesystem instead of the
// in-memory one — the shell, the file manager and the viewer all take an
// afero.Fs, so substituting it here is the whole of what makes them real.
//
// It deliberately does NOT seed. Seeding writes websh's example files, which
// is right for an empty filesystem in a tab and wrong for somebody's home
// directory: the first thing this would otherwise do is scatter demo files
// across a real machine.
//
// Calling it after the filesystem is in use does nothing, so the choice is made
// once, at startup, by whoever composed the desk.
func SetFS(f afero.Fs) {
	fsOnce.Do(func() { sharedFS = f })
}

// Pane is a terminal running a shell.
type Pane struct {
	greeting string
	host     string
	run      []string
	session  *web.Session
	el       js.Value // what Mount rendered into; Canvas looks inside it
}

// New makes a terminal pane. An empty host is named for the desk.
func New(greeting, host string) *Pane {
	if host == "" {
		host = "desk"
	}
	return &Pane{greeting: greeting, host: host}
}

// Run queues command lines to be submitted once the shell is up, as though
// they had been typed. It is what lets a link carry a command into the page.
func (p *Pane) Run(lines ...string) *Pane {
	p.run = append(p.run, lines...)
	return p
}

// Session is the shell running in this pane, or nil before it is mounted.
func (p *Pane) Session() *web.Session { return p.session }

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
	p.el = el
	for _, line := range p.run {
		s.Submit(line)
	}
	return nil
}

// Canvas implements desk.TexturePane: with websh's default options the terminal
// runs xterm-go's WebGL renderer, and then every glyph on screen is in that one
// canvas — which is what lets the desk composite this pane as a texture instead
// of laying it out as DOM.
//
// It returns nothing when there is no such canvas, which is not an error and is
// not rare: websh falls back to the DOM renderer wherever WebGL2 is missing,
// and so does the desk, pane by pane. The class is xterm-go's own; matching on
// it rather than on "the first canvas in here" keeps this from picking up a
// canvas some other part of the terminal might grow.
//
// Not cached. The renderer can be switched at runtime — EnableWebGL and
// DisableWebGL create and remove this element — so a cached hit would outlive
// the canvas it named, and the compositor would upload a detached element every
// frame. A querySelector over a terminal's handful of nodes is not worth
// avoiding at 60 Hz.
func (p *Pane) Canvas() js.Value {
	if !p.el.Truthy() {
		return js.Value{}
	}
	return p.el.Call("querySelector", "canvas.xterm-webgl-canvas")
}

// Close ends the session. The shared filesystem outlives it.
func (p *Pane) Close() {
	if p.session != nil {
		p.session.Close()
		p.session = nil
	}
}
