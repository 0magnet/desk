package term

import (
	"errors"
	"os"
	"testing"

	"github.com/0magnet/afero"
)

// read is the smallest thing a pane does with a filesystem it was handed once.
func read(t *testing.T, fsys afero.Fs, name string) string {
	t.Helper()
	b, err := afero.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func write(t *testing.T, fsys afero.Fs, name, body string) {
	t.Helper()
	if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// The whole point: a holder that took the filesystem BEFORE the swap sees the
// new one afterwards. websh stores it in its session, the file manager in its
// pane, and neither is asked again — so if this failed, the only way to reach
// the host filesystem would be to reload the page.
func TestASwapReachesAHolderThatTookTheFilesystemEarlier(t *testing.T) {
	mem := afero.NewMemMapFs()
	write(t, mem, "/greeting", "in the tab")

	s := &switchFs{}
	s.swap(mem)

	holder := afero.Fs(s) // what a pane keeps
	if got := read(t, holder, "/greeting"); got != "in the tab" {
		t.Fatalf("before the swap, got %q", got)
	}

	host := afero.NewMemMapFs()
	write(t, host, "/greeting", "on the machine")
	s.swap(host)

	if got := read(t, holder, "/greeting"); got != "on the machine" {
		t.Errorf("after the swap the holder still sees %q", got)
	}
}

// Writes have to land on the new filesystem too, or the shell would report
// success while putting the file somewhere nothing else can see it.
func TestASwapRedirectsWritesAsWellAsReads(t *testing.T) {
	first, second := afero.NewMemMapFs(), afero.NewMemMapFs()
	s := &switchFs{}
	s.swap(first)
	s.swap(second)

	write(t, s, "/note", "hello")
	if _, err := first.Stat("/note"); !os.IsNotExist(err) {
		t.Errorf("the write landed on the OLD filesystem (err %v)", err)
	}
	if got := read(t, second, "/note"); got != "hello" {
		t.Errorf("second filesystem has %q", got)
	}
}

// A nil backing must not panic. It should be unreachable — FS seeds one and
// SetFS installs one — but in wasm a panic is not a broken pane, it is a blank
// page, so the failure mode has to be an error.
func TestNoBackingIsAnErrorRatherThanAPanic(t *testing.T) {
	s := &switchFs{}
	if _, err := s.Open("/anything"); err == nil {
		t.Error("Open with no backing returned no error")
	} else if !errors.Is(err, os.ErrInvalid) {
		t.Errorf("Open with no backing: %v, want ErrInvalid", err)
	}
	if err := s.Mkdir("/d", 0o755); !errors.Is(err, os.ErrInvalid) {
		t.Errorf("Mkdir with no backing: %v, want ErrInvalid", err)
	}
	if name := s.Name(); name == "" {
		t.Error("Name with no backing was empty")
	}
}

// afero calls os.IsNotExist rather than errors.Is in places, and that predates
// wrapping: it unwraps exactly one level. A filesystem behind the switch must
// keep answering it the same way, or a missing file starts reading as a real
// failure — see hostfs/decode.go, where this cost real debugging.
func TestNotExistSurvivesTheSwitch(t *testing.T) {
	s := &switchFs{}
	s.swap(afero.NewMemMapFs())
	_, err := s.Open("/nothing/here")
	if !os.IsNotExist(err) {
		t.Errorf("os.IsNotExist said no about %v", err)
	}
}
