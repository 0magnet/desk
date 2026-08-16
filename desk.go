//go:build js && wasm

// Package desk arranges panes in windows.
//
// It is deliberately small, and knows nothing about terminals, images or
// anything else it might end up holding. winbox-go supplies the windows; a
// pane is anything that can render into a DOM element. Programs register the
// panes they have and ask for one by name; the desk creates a window, mounts
// the pane in it, and cleans up when the window closes.
//
// Keeping the shell ignorant of its contents is the point. A terminal is one
// pane, an image viewer is another, and neither had to be anticipated here —
// which is what lets a project bring its own pane rather than waiting for this
// package to grow support for it.
package desk

import (
	"fmt"
	"sort"
	"sync"
	"syscall/js"

	winbox "github.com/0magnet/winbox-go"
)

// Pane is something that can live in a window.
type Pane interface {
	// Mount renders the pane into el, the window's content element. The
	// element is empty and sized before Mount is called.
	Mount(el js.Value) error

	// Close releases whatever Mount acquired. It is called when the window
	// closes, and must tolerate a pane that failed to mount or never did.
	Close()
}

// Resizer is implemented by panes that need to be told their size changed.
//
// Most do not. A pane laid out with CSS is resized by the browser, and one that
// watches its own element — as a terminal does, since it has to convert pixels
// into a character grid — hears about it from a ResizeObserver without anyone
// passing the message along. Implement this only when neither is true.
type Resizer interface {
	Resize(width, height float64)
}

// App is a registered pane type: what to call it, and how to make one.
type App struct {
	Name  string // the identifier passed to Launch
	Title string // the window title
	Icon  string // optional icon URL
	Help  string // one line, for a launcher

	// Width and Height are the initial window size. Zero picks a default.
	Width, Height winbox.Unit

	// Open builds a pane. args are whatever Launch was given, so an app can
	// take a filename or a mode without the desk knowing what either means.
	Open func(args []string) (Pane, error)
}

var (
	mu   sync.Mutex
	apps = map[string]App{}
	root js.Value // where windows are placed; zero means the document body
)

// SetRoot confines windows to an element. Without it winbox places them
// against the body, which is wrong the moment the page has a header: windows
// can be dragged up underneath it and their maximised size is a header too
// tall.
func SetRoot(el js.Value) {
	mu.Lock()
	defer mu.Unlock()
	root = el
}

// Register adds an app, replacing any with the same name.
func Register(a App) {
	mu.Lock()
	defer mu.Unlock()
	apps[a.Name] = a
}

// Apps lists the registered apps by name.
func Apps() []App {
	mu.Lock()
	defer mu.Unlock()
	out := make([]App, 0, len(apps))
	for _, a := range apps {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup finds a registered app.
func Lookup(name string) (App, bool) {
	mu.Lock()
	defer mu.Unlock()
	a, ok := apps[name]
	return a, ok
}

// Options adjust a single launch.
type Options struct {
	Title         string // overrides the app's title
	Width, Height winbox.Unit
	X, Y          winbox.Unit
}

// Launch opens a window holding a new pane of the named app.
func Launch(name string, args ...string) (*winbox.WinBox, error) {
	return LaunchOpts(name, Options{}, args...)
}

// LaunchOpts is Launch with the window's placement spelled out.
func LaunchOpts(name string, opt Options, args ...string) (*winbox.WinBox, error) {
	app, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("desk: no app named %q", name)
	}
	pane, err := app.Open(args)
	if err != nil {
		return nil, err
	}

	title := app.Title
	if opt.Title != "" {
		title = opt.Title
	}
	w, h := app.Width, app.Height
	if opt.Width != (winbox.Unit{}) {
		w = opt.Width
	}
	if opt.Height != (winbox.Unit{}) {
		h = opt.Height
	}
	if w == (winbox.Unit{}) {
		w = winbox.Px(720)
	}
	if h == (winbox.Unit{}) {
		h = winbox.Px(460)
	}

	mu.Lock()
	r := root
	mu.Unlock()

	// Keep windows clear of the panel, so a window dragged to the bottom does
	// not end up with its own controls underneath the task buttons.
	var bottom winbox.Unit
	if panel != nil {
		bottom = winbox.Px(PanelHeight)
	}

	o := &winbox.Options{
		Root:   r,
		Bottom: bottom,
		Title:  title,
		Icon:   app.Icon,
		Width:  w,
		Height: h,
		X:      opt.X,
		Y:      opt.Y,
		// The pane owns the body's contents; nothing here should scroll it.
		Background: "#1b1f27",
	}
	if opt.X == (winbox.Unit{}) && opt.Y == (winbox.Unit{}) {
		o.X, o.Y = cascade()
	}

	win := winbox.New(o)

	// Mount after creation: the body exists and has been sized by now, which
	// a pane that measures itself in pixels depends on.
	if err := pane.Mount(win.Body); err != nil {
		win.Close(true)
		pane.Close()
		return nil, err
	}

	if r, ok := pane.(Resizer); ok {
		win.OnResize = func(_ *winbox.WinBox, width, height float64) {
			r.Resize(width, height)
		}
	}
	if panel != nil {
		alive := true
		tracked := &Window{
			Title: title,
			Focus: func() { win.Restore().Focus() },
			Alive: func() bool { return alive },
		}
		panel.Track(tracked)
		panel.SetActive(tracked) // a window opens focused

		prevFocus := win.OnFocus
		win.OnFocus = func(wb *winbox.WinBox) {
			if prevFocus != nil {
				prevFocus(wb)
			}
			panel.SetActive(tracked)
		}

		prev := win.OnClose
		win.OnClose = func(wb *winbox.WinBox, force bool) bool {
			if prev != nil && prev(wb, force) {
				return true
			}
			alive = false
			panel.SetActive(nil)
			return false
		}
	}

	prevClose := win.OnClose
	win.OnClose = func(wb *winbox.WinBox, force bool) bool {
		if prevClose != nil && prevClose(wb, force) {
			return true // a handler vetoed it; the pane stays alive
		}
		pane.Close()
		return false
	}
	return win, nil
}

// cascade staggers new windows so a second one does not land exactly on the
// first. It is the whole of the desk's window-placement policy.
var cascadeN int

func cascade() (winbox.Unit, winbox.Unit) {
	const step = 28
	n := cascadeN % 8
	cascadeN++
	return winbox.Px(float64(60 + n*step)), winbox.Px(float64(50 + n*step))
}
