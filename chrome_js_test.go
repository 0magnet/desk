//go:build js && wasm

package desk

import (
	"syscall/js"
	"testing"
)

// chromeFor needs a document but no WebGL — it draws with Canvas2D — so a bare
// compositor is enough to test the part that matters: the cache. Rasterizing
// text is the expensive thing here, and doing it once per change rather than
// once per frame is the whole design. A cache that silently stops hitting
// looks identical on screen.

func testCompositor(t *testing.T) *compositor {
	t.Helper()
	if !js.Global().Get("document").Truthy() {
		t.Skip("no document; run with make test-browser")
	}
	return &compositor{chrome: map[string]chromeCache{}, dpr: 1}
}

func bar() rect { return rect{X: 0, Y: 0, W: 320, H: 35} }

func TestChromeIsRasterizedOnce(t *testing.T) {
	c := testCompositor(t)

	first := c.chromeFor("w1", "terminal", true, bar())
	if !first.Truthy() {
		t.Fatal("no canvas was produced")
	}
	second := c.chromeFor("w1", "terminal", true, bar())
	if !second.Equal(first) {
		t.Error("the same title bar was rasterized twice; the cache is not hitting")
	}
}

func TestChromeIsRedrawnWhenSomethingVisibleChanges(t *testing.T) {
	c := testCompositor(t)
	base := c.chromeFor("w1", "terminal", true, bar())

	if again := c.chromeFor("w1", "files", true, bar()); again.Equal(base) {
		t.Error("a renamed window kept its old title bar")
	}
	c = testCompositor(t)
	base = c.chromeFor("w1", "terminal", true, bar())
	if again := c.chromeFor("w1", "terminal", false, bar()); again.Equal(base) {
		t.Error("losing focus did not redraw the bar")
	}
	c = testCompositor(t)
	base = c.chromeFor("w1", "terminal", true, bar())
	wider := bar()
	wider.W = 640
	if again := c.chromeFor("w1", "terminal", true, wider); again.Equal(base) {
		t.Error("a resized window kept a bar of the old width")
	}
}

func TestChromeCanvasIsTheSizeItWasAskedFor(t *testing.T) {
	// In DEVICE pixels: a bar rasterized at CSS size on a 2x display is a
	// blurry bar, and blurry is exactly what a texture makes obvious.
	c := testCompositor(t)
	c.dpr = 2
	cv := c.chromeFor("w1", "terminal", true, bar())
	if !cv.Truthy() {
		t.Fatal("no canvas")
	}
	if got, want := cv.Get("width").Int(), 640; got != want {
		t.Errorf("canvas width %d, want %d", got, want)
	}
	if got, want := cv.Get("height").Int(), 70; got != want {
		t.Errorf("canvas height %d, want %d", got, want)
	}
}

func TestChromeRefusesAnEmptyBar(t *testing.T) {
	c := testCompositor(t)
	if cv := c.chromeFor("w1", "terminal", true, rect{}); cv.Truthy() {
		t.Error("a zero-sized bar produced a canvas")
	}
}

func TestDropChromeForgetsAClosedWindow(t *testing.T) {
	// The same reason dropTexture exists: a desk that has opened and closed a
	// hundred windows should not be holding a hundred canvases.
	c := testCompositor(t)
	c.chromeFor("w1", "terminal", true, bar())
	if len(c.chrome) != 1 {
		t.Fatalf("cache holds %d entries, want 1", len(c.chrome))
	}
	c.dropChrome("w1")
	if len(c.chrome) != 0 {
		t.Errorf("cache still holds %d entries after dropping", len(c.chrome))
	}
}

func TestEachWindowGetsItsOwnBar(t *testing.T) {
	c := testCompositor(t)
	a := c.chromeFor("w1", "terminal", true, bar())
	b := c.chromeFor("w2", "files", true, bar())
	if a.Equal(b) {
		t.Error("two windows share one title bar canvas")
	}
	if len(c.chrome) != 2 {
		t.Errorf("cache holds %d entries, want 2", len(c.chrome))
	}
}
