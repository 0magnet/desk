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

	// Width and Height are the preferred window size in pixels. Zero picks a
	// default. Both are capped at what the desk can actually show, so this is
	// what the app would like rather than what it will get.
	Width, Height float64

	// Maximized opens the window filling the desk. Worth setting for whatever
	// a page opens with: a small window adrift in a large empty desktop reads
	// as something that failed to size itself, not as a window manager.
	Maximized bool

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
// can be dragged up underneath it and their maximized size is a header too
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

// Options adjust a single launch. Sizes and positions are pixels; zero means
// "use the app's preference", and for X and Y "place it automatically".
type Options struct {
	Title         string // overrides the app's title
	Width, Height float64
	X, Y          float64

	// Maximized overrides the app's own setting when true.
	Maximized bool
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
	if opt.Width > 0 {
		w = opt.Width
	}
	if opt.Height > 0 {
		h = opt.Height
	}
	if w <= 0 {
		w = 720
	}
	if h <= 0 {
		h = 460
	}

	mu.Lock()
	r := root
	mu.Unlock()

	// An app asks for the size it would like, not the size it can have. On a
	// phone the desk is narrower than any sensible default, and a window
	// opened at its preferred width would hang off the right edge with its
	// own controls out of reach.
	//
	// Where it goes has to be settled first: a window is clamped to the room
	// left after its offset, not to the whole desk, or a cascaded window
	// overflows by exactly the amount it was staggered.
	// Windows are position:fixed, so their coordinates are the viewport's and
	// not the root element's — appending into the root sets parentage, not
	// where the window lands. The root's offset therefore has to be added
	// here, and declared to winbox as a viewport limit so a window cannot be
	// dragged out over the page's own furniture.
	dk := deskRect(r)
	x, y := opt.X, opt.Y
	if x <= 0 && y <= 0 {
		x, y = cascade(dk.w, dk.h)
	}
	w = clamp(w, dk.w-x-8)
	h = clamp(h, dk.h-y-PanelHeight-8)
	x += dk.left
	y += dk.top

	// Keep windows inside the desk, and clear of the panel so one dragged to
	// the bottom does not end up with its controls under the task buttons.
	bottomLimit := dk.bottom
	if panel != nil {
		bottomLimit += PanelHeight
	}

	o := &winbox.Options{
		Root: r,
		// Maximizing respects the viewport limits below, so it fills the desk
		// rather than the page.
		Max:    app.Maximized || opt.Maximized,
		Top:    winbox.Px(dk.top),
		Left:   winbox.Px(dk.left),
		Right:  winbox.Px(dk.right),
		Bottom: winbox.Px(bottomLimit),
		Title:  title,
		Icon:   app.Icon,
		Width:  winbox.Px(w),
		Height: winbox.Px(h),
		// The pane owns the body's contents; nothing here should scroll it.
		Background: "#1b1f27",
	}
	o.X, o.Y = winbox.Px(x), winbox.Px(y)

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

// deskBox is where the desk sits in the viewport, and how much of the viewport
// is not it.
type deskBox struct {
	left, top     float64 // the desk's own offset
	right, bottom float64 // what is left of the viewport past its far edges
	w, h          float64
}

// deskRect measures the desk. Everything about window placement is expressed
// against the viewport, so a desk that is not the whole page has to say by how
// much.
func deskRect(r js.Value) deskBox {
	el := r
	if !el.Truthy() {
		el = js.Global().Get("document").Get("body")
	}
	rect := el.Call("getBoundingClientRect")
	w, h := rect.Get("width").Float(), rect.Get("height").Float()
	if w <= 0 || h <= 0 {
		return deskBox{w: 1024, h: 700} // nothing laid out yet; guess generously
	}
	win := js.Global()
	vw, vh := win.Get("innerWidth").Float(), win.Get("innerHeight").Float()
	return deskBox{
		left:   rect.Get("left").Float(),
		top:    rect.Get("top").Float(),
		right:  vw - rect.Get("right").Float(),
		bottom: vh - rect.Get("bottom").Float(),
		w:      w,
		h:      h,
	}
}

// clamp caps a size at what is available, with a floor so a window is never
// shrunk to something unusable on a very small screen — better to overflow a
// little than to be a sliver.
func clamp(v, max float64) float64 {
	if max < 220 {
		max = 220
	}
	if v > max {
		return max
	}
	return v
}

// cascade staggers new windows so a second one does not land exactly on the
// first, and gives up staggering when there is no room to stagger into.
var cascadeN int

func cascade(availW, availH float64) (float64, float64) {
	if availW < 700 || availH < 520 {
		return 8, 8 // no room to stagger into
	}
	const step = 28
	n := cascadeN % 8
	cascadeN++
	return float64(60 + n*step), float64(50 + n*step)
}
