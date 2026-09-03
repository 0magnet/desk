//go:build js && wasm

package desk

import (
	"strings"
	"testing"
)

// clean empties the app registry, which is package state shared by every test.
func clean(t *testing.T) {
	t.Helper()
	mu.Lock()
	apps = map[string]App{}
	mu.Unlock()
	cascadeN = 0
	t.Cleanup(func() {
		mu.Lock()
		apps = map[string]App{}
		mu.Unlock()
	})
}

func TestRegisterAndLookup(t *testing.T) {
	clean(t)
	Register(App{Name: "term", Help: "a terminal"})

	got, ok := Lookup("term")
	if !ok {
		t.Fatal("a registered app was not found")
	}
	if got.Help != "a terminal" {
		t.Errorf("Help = %q, want the registered one", got.Help)
	}
	if _, ok := Lookup("nosuch"); ok {
		t.Error("an app that was never registered was found")
	}
}

// Registering the same name twice replaces rather than duplicating, so a
// rebuild of an app does not leave two entries in the menu.
func TestRegisterReplacesByName(t *testing.T) {
	clean(t)
	Register(App{Name: "term", Help: "first"})
	Register(App{Name: "term", Help: "second"})

	if n := len(Apps()); n != 1 {
		t.Errorf("the registry holds %d apps, want 1", n)
	}
	got, _ := Lookup("term")
	if got.Help != "second" {
		t.Errorf("Help = %q, want the later registration", got.Help)
	}
}

// The menu is built from this list, so it has to be in a stable order rather
// than the map's.
func TestAppsAreSortedByName(t *testing.T) {
	clean(t)
	for _, n := range []string{"zed", "alpha", "mid", "Beta"} {
		Register(App{Name: n})
	}
	var names []string
	for _, a := range Apps() {
		names = append(names, a.Name)
	}
	want := "Beta,alpha,mid,zed" // capitals sort first, as byte order does
	if got := strings.Join(names, ","); got != want {
		t.Errorf("Apps() = %q, want %q", got, want)
	}
}

func TestAppsIsStableAcrossCalls(t *testing.T) {
	clean(t)
	for _, n := range []string{"c", "a", "b", "e", "d"} {
		Register(App{Name: n})
	}
	first := Apps()
	for i := 0; i < 20; i++ {
		again := Apps()
		for j := range first {
			if first[j].Name != again[j].Name {
				t.Fatalf("call %d differs at %d: %q vs %q", i, j, again[j].Name, first[j].Name)
			}
		}
	}
}

func TestAppsOnAnEmptyRegistry(t *testing.T) {
	clean(t)
	if got := Apps(); len(got) != 0 {
		t.Errorf("an empty registry listed %d apps", len(got))
	}
}

// ── geometry ─────────────────────────────────────────────────────────────────

// A window is capped at the space available, but never shrunk below something
// usable: on a small screen overflowing a little beats being a sliver.
func TestClampCapsAtTheSpaceAvailable(t *testing.T) {
	if got := clamp(500, 400); got != 400 {
		t.Errorf("clamp(500, 400) = %v, want 400", got)
	}
	if got := clamp(300, 400); got != 300 {
		t.Errorf("clamp(300, 400) = %v, want the requested 300", got)
	}
}

func TestClampHasAFloorSoAWindowIsNeverASliver(t *testing.T) {
	for _, max := range []float64{0, 50, 219, -100} {
		if got := clamp(500, max); got != 220 {
			t.Errorf("clamp(500, %v) = %v, want the 220 floor", max, got)
		}
	}
	// The floor is a floor on the cap, not on the request: asking for less
	// than the floor still gets what was asked for.
	if got := clamp(100, 0); got != 100 {
		t.Errorf("clamp(100, 0) = %v, want the requested 100", got)
	}
}

// Windows stagger so a second one does not land exactly on the first.
func TestCascadeStaggersSuccessiveWindows(t *testing.T) {
	clean(t)
	x1, y1 := cascade(1200, 900)
	x2, y2 := cascade(1200, 900)
	if x1 == x2 || y1 == y2 {
		t.Errorf("two windows landed at the same offset: %v,%v and %v,%v", x1, y1, x2, y2)
	}
	if x2-x1 != 28 || y2-y1 != 28 {
		t.Errorf("the stagger is %v,%v, want 28,28", x2-x1, y2-y1)
	}
}

// The stagger wraps rather than walking off the screen forever.
func TestCascadeWrapsAfterEightWindows(t *testing.T) {
	clean(t)
	first, _ := cascade(1200, 900)
	for i := 0; i < 7; i++ {
		cascade(1200, 900)
	}
	ninth, _ := cascade(1200, 900)
	if ninth != first {
		t.Errorf("the ninth window is at %v, want back to the first offset %v", ninth, first)
	}
}

// On a small screen there is nowhere to stagger into, so windows go to the
// corner instead of marching off the edge.
func TestCascadeGivesUpOnASmallScreen(t *testing.T) {
	clean(t)
	for _, tc := range []struct{ w, h float64 }{
		{699, 900}, {1200, 519}, {320, 480}, {0, 0},
	} {
		x, y := cascade(tc.w, tc.h)
		if x != 8 || y != 8 {
			t.Errorf("cascade(%v, %v) = %v,%v want the 8,8 corner", tc.w, tc.h, x, y)
		}
	}
}

// Giving up must not consume a stagger slot, or a run of small-screen launches
// silently advances the position for later ones.
func TestCascadeOnASmallScreenDoesNotAdvanceTheStagger(t *testing.T) {
	clean(t)
	before, _ := cascade(1200, 900)
	cascade(100, 100)
	cascade(100, 100)
	after, _ := cascade(1200, 900)
	if after-before != 28 {
		t.Errorf("the stagger moved by %v across two small-screen launches, want 28", after-before)
	}
}

// ── search ───────────────────────────────────────────────────────────────────

func TestContainsFoldIgnoresCase(t *testing.T) {
	for _, tc := range []struct {
		s, sub string
		want   bool
	}{
		{"Terminal", "term", true},
		{"Terminal", "TERM", true},
		{"Terminal", "MIN", true},
		{"Terminal", "xyz", false},
		{"Terminal", "", true}, // an empty query matches everything
		{"", "", true},
		{"", "a", false},
		{"abc", "abc", true},
		{"abc", "abcd", false},
	} {
		if got := containsFold(tc.s, tc.sub); got != tc.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}

func TestLowerOnlyFoldsASCII(t *testing.T) {
	if got := lower("ABC-123_xyz"); got != "abc-123_xyz" {
		t.Errorf("lower = %q", got)
	}
	// Non-ASCII is left alone rather than mangled a byte at a time.
	if got := lower("Ünïcode"); !strings.Contains(got, "code") {
		t.Errorf("lower mangled non-ASCII: %q", got)
	}
}
