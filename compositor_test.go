// No build tag, like the file it tests. The browser half cannot be tested at
// all — Node has no canvas and no WebGL — so everything worth being sure about
// was pushed into compositor.go, and this is where being sure happens.
package desk

import (
	"math"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func sameRect(a, b rect) bool {
	return near(a.X, b.X) && near(a.Y, b.Y) && near(a.W, b.W) && near(a.H, b.H)
}

func ids(ds []quadDraw) string {
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(d.ID)
	}
	return b.String()
}

// canvasWin is a window that can be composited: sized, visible, with a texture.
func canvasWin(id string, z int, r rect) winState {
	return winState{ID: id, Z: z, Rect: r, Header: 35, TexW: 800, TexH: 600}
}

// domWin is a pane that is not canvas-backed. Everything else about it is the
// same, which is the case that matters: it is excluded for that reason alone.
func domWin(id string, z int, r rect) winState {
	return winState{ID: id, Z: z, Rect: r, Header: 35}
}

// ── clip space ───────────────────────────────────────────────────────────────

// The one conversion the shader does not do. y is flipped and x is not, which
// is the mistake that puts every window upside down at the wrong end of the
// desk, so it is pinned corner by corner.
func TestClipRectMapsTheDeskToClipSpace(t *testing.T) {
	full := clipRect(rect{X: 0, Y: 0, W: 1000, H: 500}, 1000, 500)
	want := [4]float32{-1, 1, 1, -1}
	if full != want {
		t.Errorf("a full-desk quad = %v, want %v (top left to bottom right)", full, want)
	}

	// The middle quarter, centered: clip space is symmetric so it comes back
	// symmetric.
	mid := clipRect(rect{X: 250, Y: 125, W: 500, H: 250}, 1000, 500)
	wantMid := [4]float32{-0.5, 0.5, 0.5, -0.5}
	if mid != wantMid {
		t.Errorf("a centered quad = %v, want %v", mid, wantMid)
	}
}

func TestClipRectPutsYTheRightWayUp(t *testing.T) {
	// Top half of the desk: y goes from 1 (top) down to 0 (middle).
	top := clipRect(rect{X: 0, Y: 0, W: 100, H: 50}, 100, 100)
	if top[1] <= top[3] {
		t.Errorf("y0 %v is not above y1 %v; the quad is upside down", top[1], top[3])
	}
	if !near(float64(top[1]), 1) || !near(float64(top[3]), 0) {
		t.Errorf("the top half spans y %v..%v, want 1..0", top[1], top[3])
	}
	// x is not flipped: the left edge stays on the left.
	if top[0] >= top[2] {
		t.Errorf("x0 %v is not left of x1 %v", top[0], top[2])
	}
}

// A desk with no size yet — measured before layout, or in a hidden tab —
// must not divide by zero and hand the shader a NaN, which draws nothing at all
// and gives no clue why.
func TestClipRectSurvivesAnUnmeasuredDesk(t *testing.T) {
	for _, v := range [][2]float64{{0, 100}, {100, 0}, {0, 0}, {-5, -5}} {
		got := clipRect(rect{W: 10, H: 10}, v[0], v[1])
		for _, f := range got {
			if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
				t.Errorf("clipRect with a %vx%v view = %v", v[0], v[1], got)
				break
			}
		}
	}
}

// ── chrome and fit ───────────────────────────────────────────────────────────

func TestContentBoxIsTheWindowLessItsChrome(t *testing.T) {
	got := contentBox(rect{X: 10, Y: 20, W: 400, H: 300}, 35, 2)
	want := rect{X: 12, Y: 55, W: 396, H: 263}
	if !sameRect(got, want) {
		t.Errorf("contentBox = %+v, want %+v", got, want)
	}
}

// A window dragged down to nothing, or one whose chrome is taller than it is,
// must give back an empty box rather than a negative one: a negative width
// becomes a quad wound the other way, which draws as a mirrored window.
func TestContentBoxNeverGoesNegative(t *testing.T) {
	got := contentBox(rect{W: 10, H: 10}, 35, 8)
	if got.W < 0 || got.H < 0 {
		t.Errorf("contentBox = %+v, want no negative extent", got)
	}
	if !got.empty() {
		t.Errorf("contentBox = %+v, want an empty box", got)
	}
}

func TestFitRectPreservesAspect(t *testing.T) {
	// A 2:1 texture in a square box: full width, half height, centered.
	got := fitRect(rect{X: 0, Y: 0, W: 400, H: 400}, 800, 400)
	want := rect{X: 0, Y: 100, W: 400, H: 200}
	if !sameRect(got, want) {
		t.Errorf("fitRect = %+v, want %+v", got, want)
	}

	// And the other way round: a tall texture in a square box.
	got = fitRect(rect{X: 0, Y: 0, W: 400, H: 400}, 400, 800)
	want = rect{X: 100, Y: 0, W: 200, H: 400}
	if !sameRect(got, want) {
		t.Errorf("fitRect = %+v, want %+v", got, want)
	}
}

// The usual case: a terminal canvas whose backing store is device pixels while
// the box is CSS pixels. Only the ratio matters, so a 2x display must not put
// the terminal in a quarter of its window.
func TestFitRectIgnoresDevicePixelScaling(t *testing.T) {
	box := rect{X: 5, Y: 5, W: 600, H: 300}
	oneX := fitRect(box, 600, 300)
	twoX := fitRect(box, 1200, 600)
	if !sameRect(oneX, twoX) {
		t.Errorf("1x fit %+v and 2x fit %+v differ", oneX, twoX)
	}
	if !sameRect(oneX, box) {
		t.Errorf("a matching aspect was letterboxed: %+v, want %+v", oneX, box)
	}
}

func TestFitRectWithNothingToFit(t *testing.T) {
	box := rect{X: 1, Y: 2, W: 30, H: 40}
	for _, tc := range [][2]float64{{0, 100}, {100, 0}, {-1, -1}} {
		if got := fitRect(box, tc[0], tc[1]); !sameRect(got, box) {
			t.Errorf("fitRect(%v) = %+v, want the box unchanged", tc, got)
		}
	}
	if got := fitRect(rect{}, 10, 10); !got.empty() {
		t.Errorf("fitRect on an empty box = %+v, want it empty", got)
	}
}

// ── the plan ─────────────────────────────────────────────────────────────────

// Window coordinates are the viewport's, because winbox windows are
// position:fixed; the GL canvas covers only the desk. A desk under a page
// header is the case that catches a missing translation.
func TestPlanTranslatesViewportIntoDeskCoordinates(t *testing.T) {
	view := rect{X: 0, Y: 60, W: 1000, H: 640}
	plan := planFrame(view, []winState{canvasWin("a", 1, rect{X: 100, Y: 160, W: 400, H: 300})})
	if len(plan.Draws) != 1 {
		t.Fatalf("%d draws, want 1", len(plan.Draws))
	}
	if got := plan.Draws[0].Frame; !sameRect(got, rect{X: 100, Y: 100, W: 400, H: 300}) {
		t.Errorf("frame = %+v, want the desk's own offset taken off", got)
	}
	if got := plan.Draws[0].Title; !sameRect(got, rect{X: 100, Y: 100, W: 400, H: 35}) {
		t.Errorf("title = %+v, want the top 35px of the frame", got)
	}
}

func TestPlanDrawsBackToFront(t *testing.T) {
	r := rect{W: 300, H: 200}
	plan := planFrame(rect{W: 1000, H: 700}, []winState{
		canvasWin("c", 30, r), canvasWin("a", 10, r), canvasWin("b", 20, r),
	})
	if got := ids(plan.Draws); got != "a,b,c" {
		t.Errorf("draw order = %q, want a,b,c — lowest z first", got)
	}
}

// Two windows that have never been focused share a z-index. If the order they
// come back in wanders, they swap places from frame to frame and flicker.
func TestPlanIsStableForEqualZ(t *testing.T) {
	r := rect{W: 300, H: 200}
	in := []winState{canvasWin("a", 5, r), canvasWin("b", 5, r), canvasWin("c", 5, r)}
	first := ids(planFrame(rect{W: 1000, H: 700}, in).Draws)
	if first != "a,b,c" {
		t.Errorf("draw order = %q, want the tracking order kept", first)
	}
	for i := 0; i < 10; i++ {
		if got := ids(planFrame(rect{W: 1000, H: 700}, in).Draws); got != first {
			t.Fatalf("pass %d gave %q, want %q every time", i, got, first)
		}
	}
}

// planFrame must not reorder its caller's slice. The compositor builds that
// slice fresh each frame, but a sort in place would still be a trap for the
// next caller, and it is what makes the stability above testable at all.
func TestPlanDoesNotDisturbTheInput(t *testing.T) {
	r := rect{W: 300, H: 200}
	in := []winState{canvasWin("c", 30, r), canvasWin("a", 10, r)}
	planFrame(rect{W: 1000, H: 700}, in)
	if in[0].ID != "c" || in[1].ID != "a" {
		t.Errorf("the input was reordered: %q, %q", in[0].ID, in[1].ID)
	}
}

// The rule the whole design turns on. The GL canvas is one layer above every
// DOM window, so a composited window paints over a DOM one whatever the
// z-indexes say — only an unbroken run at the top of the stack can be drawn
// without putting the wrong window in front.
func TestPlanCompositesTheTopmostUnbrokenRun(t *testing.T) {
	r := rect{W: 300, H: 200}
	plan := planFrame(rect{W: 1000, H: 700}, []winState{
		canvasWin("bottom", 10, r),
		domWin("middle", 20, r),
		canvasWin("top", 30, r),
		canvasWin("topper", 40, r),
	})
	if got := ids(plan.Draws); got != "top,topper" {
		t.Errorf("drawn = %q, want the run above the DOM window", got)
	}
	if got := strings.Join(plan.DOM, ","); got != "bottom,middle" {
		t.Errorf("left to the DOM = %q, want everything at or below it", got)
	}
}

// A DOM pane on top means nothing can be composited. That is the whole desk
// falling back, and it is the right way round: too little compositing looks
// like nothing happened, too much looks broken.
func TestPlanFallsBackEntirelyWhenADOMWindowIsInFront(t *testing.T) {
	r := rect{W: 300, H: 200}
	plan := planFrame(rect{W: 1000, H: 700}, []winState{
		canvasWin("a", 10, r), canvasWin("b", 20, r), domWin("front", 30, r),
	})
	if len(plan.Draws) != 0 {
		t.Errorf("drew %q, want nothing composited", ids(plan.Draws))
	}
	if len(plan.DOM) != 3 {
		t.Errorf("%d windows left to the DOM, want all 3", len(plan.DOM))
	}
}

// A minimized window paints nothing, so it cannot get the order wrong and must
// not break the run — otherwise minimizing a file manager would silently switch
// compositing off for everything under it.
func TestPlanIgnoresHiddenWindows(t *testing.T) {
	r := rect{W: 300, H: 200}
	hidden := domWin("minimized", 25, r)
	hidden.Hidden = true
	plan := planFrame(rect{W: 1000, H: 700}, []winState{
		canvasWin("a", 20, r), hidden, canvasWin("b", 30, r),
	})
	if got := ids(plan.Draws); got != "a,b" {
		t.Errorf("drawn = %q, want both canvas windows", got)
	}
	if got := strings.Join(plan.DOM, ","); got != "minimized" {
		t.Errorf("left to the DOM = %q, want the minimized window", got)
	}
}

// A canvas that has not been sized yet is the normal state for the first frame
// or two after a pane mounts. It has to read as "not now" rather than as an
// empty texture, or the window flashes black before its first paint.
func TestPlanWaitsForACanvasWithNoSizeYet(t *testing.T) {
	r := rect{W: 300, H: 200}
	notYet := canvasWin("waiting", 20, r)
	notYet.TexW, notYet.TexH = 0, 0
	plan := planFrame(rect{W: 1000, H: 700}, []winState{canvasWin("a", 10, r), notYet})
	if len(plan.Draws) != 0 {
		t.Errorf("drew %q while a window above had no texture", ids(plan.Draws))
	}
}

// A window collapsed to nothing has no quad to draw. Zero-sized windows come up
// during winbox's maximize transition, so this is not hypothetical.
func TestPlanSkipsWindowsWithNoArea(t *testing.T) {
	empty := canvasWin("empty", 30, rect{X: 5, Y: 5})
	plan := planFrame(rect{W: 1000, H: 700}, []winState{
		canvasWin("a", 10, rect{W: 300, H: 200}), empty,
	})
	if len(plan.Draws) != 0 {
		t.Errorf("drew %q, want the empty window to break the run", ids(plan.Draws))
	}
	if len(plan.DOM) != 2 {
		t.Errorf("%d windows left to the DOM, want 2", len(plan.DOM))
	}
}

func TestPlanOnAnEmptyDesk(t *testing.T) {
	plan := planFrame(rect{W: 1000, H: 700}, nil)
	if len(plan.Draws) != 0 || len(plan.DOM) != 0 {
		t.Errorf("an empty desk planned %d draws and %d DOM windows", len(plan.Draws), len(plan.DOM))
	}
}

// Every window ends up in exactly one of the two lists. Anything else is a
// window that is neither drawn nor shown — a blank hole in the desk, which is
// the one outcome the fallback exists to prevent.
func TestPlanAccountsForEveryWindowExactlyOnce(t *testing.T) {
	r := rect{W: 300, H: 200}
	hidden := canvasWin("hidden", 15, r)
	hidden.Hidden = true
	in := []winState{
		canvasWin("a", 10, r), hidden, domWin("d", 20, r),
		canvasWin("b", 30, r), canvasWin("c", 40, r),
	}
	plan := planFrame(rect{W: 1000, H: 700}, in)

	seen := map[string]int{}
	for _, d := range plan.Draws {
		seen[d.ID]++
	}
	for _, id := range plan.DOM {
		seen[id]++
	}
	if len(seen) != len(in) {
		t.Fatalf("accounted for %d windows, want %d", len(seen), len(in))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s appears %d times across the plan, want once", id, n)
		}
	}
}

// A window with no header of its own gets winbox's, rather than a title bar of
// zero height with the pane drawn over where it should have been.
func TestDrawUsesTheDefaultHeaderWhenAWindowHasNone(t *testing.T) {
	w := canvasWin("a", 1, rect{W: 400, H: 300})
	w.Header = 0
	plan := planFrame(rect{W: 1000, H: 700}, []winState{w})
	if got := plan.Draws[0].Title.H; got != defaultHeader {
		t.Errorf("title bar height = %v, want the %v default", got, defaultHeader)
	}
}

// A window shorter than its own title bar — the tail of a resize to nothing —
// must not have a title bar hanging out of the bottom of it.
func TestDrawClampsTheTitleBarToTheWindow(t *testing.T) {
	w := canvasWin("a", 1, rect{W: 400, H: 20})
	plan := planFrame(rect{W: 1000, H: 700}, []winState{w})
	d := plan.Draws[0]
	if d.Title.H > d.Frame.H {
		t.Errorf("title bar %v is taller than the window %v", d.Title.H, d.Frame.H)
	}
}

// The texture lands inside the window, not over its chrome.
func TestDrawKeepsTheBodyInsideTheFrame(t *testing.T) {
	w := canvasWin("a", 1, rect{X: 40, Y: 40, W: 400, H: 300})
	w.Border = 2
	plan := planFrame(rect{W: 1000, H: 700}, []winState{w})
	d := plan.Draws[0]
	if d.Body.Y < d.Title.Y+d.Title.H {
		t.Errorf("body top %v is above the title bar's bottom %v", d.Body.Y, d.Title.Y+d.Title.H)
	}
	if d.Body.X < d.Frame.X || d.Body.X+d.Body.W > d.Frame.X+d.Frame.W {
		t.Errorf("body %+v is outside the frame %+v horizontally", d.Body, d.Frame)
	}
	if d.Body.Y+d.Body.H > d.Frame.Y+d.Frame.H {
		t.Errorf("body %+v hangs below the frame %+v", d.Body, d.Frame)
	}
}
