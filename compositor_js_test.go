//go:build js && wasm

package desk

import (
	"syscall/js"
	"testing"

	winbox "github.com/0magnet/winbox-go"
)

// What can be tested here is the plumbing between the desk and the compositor —
// tracking, the opacity swap, and reading a window's state — none of which need
// a canvas. The drawing itself needs WebGL and so needs a browser; Node has
// neither, and even under `make test-browser` a test that drew something could
// only assert that the calls did not throw.

// fakeEl is a stand-in for a window's DOM element: enough of one for the two
// things the compositor asks of it.
func fakeEl(t *testing.T, x, y, w, h float64) js.Value {
	t.Helper()
	rect := js.FuncOf(func(js.Value, []js.Value) any {
		return map[string]any{"left": x, "top": y, "width": w, "height": h}
	})
	t.Cleanup(rect.Release)
	return js.ValueOf(map[string]any{
		"style":                 map[string]any{},
		"isConnected":           true,
		"getBoundingClientRect": rect,
	})
}

func fakeWindow(t *testing.T, x, y, w, h float64) *winbox.WinBox {
	t.Helper()
	return &winbox.WinBox{DOM: fakeEl(t, x, y, w, h), Header: 35, Index: 12}
}

type nothingPane struct{}

func (nothingPane) Mount(js.Value) error { return nil }
func (nothingPane) Close()               {}

// canvasPane implements TexturePane, and can be asked to have nothing to give
// yet — which is the normal state for the first frames after a mount.
type canvasPane struct{ canvas js.Value }

func (canvasPane) Mount(js.Value) error { return nil }
func (canvasPane) Close()               {}
func (p canvasPane) Canvas() js.Value   { return p.canvas }

// Turning compositing on must never leave the desk worse off. Under Node there
// is no document at all, which is the same shape of failure as a browser with
// no WebGL2: an error, and nothing changed.
func TestEnableCompositingIsAllOrNothing(t *testing.T) {
	err := EnableCompositing()
	if err != nil {
		if Compositing() {
			t.Error("EnableCompositing failed but left compositing on")
		}
		return
	}
	// A real browser got here. Put it back, since the rest of the tests expect
	// the DOM path and a running animation loop would outlive them.
	if !Compositing() {
		t.Error("EnableCompositing returned nil but compositing is off")
	}
	DisableCompositing()
	if Compositing() {
		t.Error("compositing is still on after DisableCompositing")
	}
}

func TestDisableCompositingWhenItWasNeverOn(t *testing.T) {
	DisableCompositing() // must not panic on a nil compositor
	if Compositing() {
		t.Error("compositing is on after disabling it from cold")
	}
}

// The fallback is one style property, which is what makes it cheap enough to
// take on any frame that goes wrong.
func TestHidingAWindowIsOneReversibleProperty(t *testing.T) {
	lw := &liveWindow{id: "w1", win: fakeWindow(t, 0, 0, 400, 300)}
	opacity := func() string { return lw.win.DOM.Get("style").Get("opacity").String() }

	lw.hide()
	if got := opacity(); got != "0" {
		t.Errorf("opacity = %q after hide, want 0", got)
	}
	lw.hide() // idempotent: the compositor calls it every frame
	if got := opacity(); got != "0" {
		t.Errorf("opacity = %q after a second hide, want 0", got)
	}
	lw.show()
	if got := opacity(); got != "" {
		t.Errorf("opacity = %q after show, want it cleared", got)
	}
	lw.show()
	if got := opacity(); got != "" {
		t.Errorf("opacity = %q after a second show, want it cleared", got)
	}
}

// TestCompositingHidesTheBodyAndLeavesTheFrame is a regression guard for a bug
// that only a browser showed: hiding the whole window left a terminal floating
// with no title bar, no close button and no border.
//
// The compositor draws a pane's canvas at the content box. The frame around it
// — the title, the buttons, the border — is DOM, and DOM is precisely what
// cannot be sampled into a texture, so hiding it removes chrome that nothing
// then redraws. What the compositor took over is the pane; the window was never
// its business.
func TestCompositingHidesTheBodyAndLeavesTheFrame(t *testing.T) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		t.Skip("no document")
	}
	el := doc.Call("createElement", "div")
	body := doc.Call("createElement", "div")
	body.Set("className", "wb-body")
	el.Call("appendChild", body)

	win := fakeWindow(t, 0, 0, 400, 300)
	win.DOM = el
	lw := &liveWindow{id: "w1", win: win}

	lw.hide()
	if got := body.Get("style").Get("opacity").String(); got != "0" {
		t.Errorf("the body was not hidden: opacity = %q", got)
	}
	if got := el.Get("style").Get("opacity").String(); got != "" {
		t.Errorf("the frame was hidden too: opacity = %q — the title bar would vanish", got)
	}

	lw.show()
	if got := body.Get("style").Get("opacity").String(); got != "" {
		t.Errorf("the body stayed hidden: opacity = %q", got)
	}
}

// A window that closes while it is being composited must not take its opacity
// with it: whatever else holds the element — an animation, a screenshot — would
// be holding an invisible one.
func TestUntrackingRestoresAHiddenWindow(t *testing.T) {
	lw := trackWindow(fakeWindow(t, 0, 0, 400, 300), nothingPane{})
	lw.hide()
	untrackWindow(lw)

	if got := lw.win.DOM.Get("style").Get("opacity").String(); got != "" {
		t.Errorf("opacity = %q after untracking, want it cleared", got)
	}
	for _, w := range liveWindows() {
		if w == lw {
			t.Fatal("an untracked window is still in the list")
		}
	}
}

func TestTrackedWindowsHaveDistinctIDs(t *testing.T) {
	a := trackWindow(fakeWindow(t, 0, 0, 10, 10), nothingPane{})
	b := trackWindow(fakeWindow(t, 0, 0, 10, 10), nothingPane{})
	t.Cleanup(func() { untrackWindow(a); untrackWindow(b) })
	if a.id == b.id {
		t.Errorf("two windows share the id %q; they would share a texture", a.id)
	}
}

// The state a window reports is what planFrame decides on, so the two cases
// that keep a window on the DOM path have to be read correctly.
func TestStateOfAPaneThatIsNotCanvasBacked(t *testing.T) {
	lw := &liveWindow{id: "w1", win: fakeWindow(t, 30, 40, 400, 300), pane: nothingPane{}}
	st, canvas := lw.state()
	if canvas.Truthy() {
		t.Error("a DOM pane handed back a canvas")
	}
	if st.drawable() {
		t.Error("a DOM pane's window reads as drawable")
	}
	if st.Rect.X != 30 || st.Rect.Y != 40 || st.Rect.W != 400 || st.Rect.H != 300 {
		t.Errorf("rect = %+v, want the element's own box", st.Rect)
	}
	if st.Header != 35 || st.Z != 12 {
		t.Errorf("header %v and z %v, want the window's own", st.Header, st.Z)
	}
}

func TestStateOfACanvasPaneWithNoCanvasYet(t *testing.T) {
	lw := &liveWindow{id: "w1", win: fakeWindow(t, 0, 0, 400, 300), pane: canvasPane{}}
	st, canvas := lw.state()
	if canvas.Truthy() {
		t.Error("a pane with no canvas yet handed one back")
	}
	if st.drawable() {
		t.Error("a pane with no canvas yet reads as drawable")
	}
}

func TestStateOfACanvasPaneThatIsReady(t *testing.T) {
	canvas := js.ValueOf(map[string]any{"width": 1600.0, "height": 1200.0})
	lw := &liveWindow{id: "w1", win: fakeWindow(t, 0, 0, 400, 300), pane: canvasPane{canvas: canvas}}
	st, got := lw.state()
	if !got.Truthy() {
		t.Fatal("a ready canvas pane handed back nothing")
	}
	if st.TexW != 1600 || st.TexH != 1200 {
		t.Errorf("texture size = %vx%v, want the canvas backing store", st.TexW, st.TexH)
	}
	if !st.drawable() {
		t.Error("a ready canvas pane's window is not drawable")
	}
}

// A window whose element has gone — closed, or never opened — must read as
// hidden rather than as a window at the origin with no size, which would break
// the composited run for everything under it.
func TestStateOfAWindowWithNoElement(t *testing.T) {
	lw := &liveWindow{id: "w1", pane: nothingPane{}}
	st, _ := lw.state()
	if !st.Hidden {
		t.Error("a window with no element does not read as hidden")
	}
	if st.drawable() {
		t.Error("a window with no element reads as drawable")
	}
}

// Turning DrawChrome off while compositing is already running must not be
// holding the package mutex when it puts the panel back.
//
// It used to be. EnableCompositingOpts took mu and called restorePanelDOM
// inside it, which reaches rootElement, which takes mu again — and a Go mutex
// is not reentrant, so the goroutine blocked against itself. On wasm that is
// the only thread, so the page stopped dead: no panic, no console output,
// nothing on stderr, a tab that never painted again. It fired on one transition
// only, DrawChrome true to false with the compositor already up, which is what
// leaving a desk drawn as a texture does — so every exit from that mode froze
// while entering was fine.
//
// The assertion is TryLock rather than a call to the real thing. A test that
// exercises the real path reproduces the deadlock instead of reporting it: it
// hangs the suite and eventually fails on go test's ten-minute timeout, which
// is a signal nobody wants to receive. Hooking the indirection and asking
// whether the lock is free says the same thing in a millisecond.
func TestPanelIsRestoredWithoutHoldingTheLock(t *testing.T) {
	mu.Lock()
	prev := comp
	comp = &compositor{textures: map[string]js.Value{}}
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); comp = prev; mu.Unlock() })

	prevRestore := restorePanel
	t.Cleanup(func() { restorePanel = prevRestore })

	called, locked := false, false
	restorePanel = func() {
		called = true
		if mu.TryLock() {
			mu.Unlock()
		} else {
			locked = true
		}
	}

	if err := EnableCompositingOpts(CompositingOptions{DrawChrome: false}); err != nil {
		t.Fatalf("EnableCompositingOpts: %v", err)
	}
	if !called {
		t.Fatal("the panel was never put back, so a drawn panel would stay hidden and clickable")
	}
	if locked {
		t.Error("the mutex was held while the panel was put back — the real restore takes it again and deadlocks the page")
	}

	mu.Lock()
	chrome := comp.drawChrome
	mu.Unlock()
	if chrome {
		t.Error("DrawChrome is still on after being switched off")
	}
}
