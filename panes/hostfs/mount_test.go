package hostfs

import (
	"os"
	"sort"
	"testing"

	"github.com/0magnet/afero"
)

// The routing is tested against two in-memory filesystems standing in for the
// host and the synthetic /bin. Which filesystem a path lands in is the entire
// contract, and none of it needs a browser.
func two(t *testing.T) (base, bin afero.Fs, m afero.Fs) {
	t.Helper()
	base = afero.NewMemMapFs()
	bin = afero.NewMemMapFs()
	return base, bin, Mount(base, map[string]afero.Fs{"/bin": bin})
}

func TestWritesUnderAMountStayOutOfTheHost(t *testing.T) {
	// The bug this whole file exists for: websh's PopulateBin writing fifty
	// applet stubs into somebody's home directory.
	base, bin, m := two(t)

	if err := m.MkdirAll("/bin", 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	if err := afero.WriteFile(m, "/bin/ls", []byte("stub"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := bin.Stat("/bin/ls"); err != nil {
		t.Errorf("the stub did not land in the mount: %v", err)
	}
	if _, err := base.Stat("/bin/ls"); !os.IsNotExist(err) {
		t.Errorf("the stub reached the host filesystem: %v", err)
	}
	if _, err := base.Stat("/bin"); !os.IsNotExist(err) {
		t.Errorf("even the directory reached the host filesystem: %v", err)
	}
}

func TestEverythingElseGoesToTheHost(t *testing.T) {
	base, bin, m := two(t)

	if err := afero.WriteFile(m, "/notes.txt", []byte("real"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := base.Stat("/notes.txt"); err != nil {
		t.Errorf("a normal file missed the host: %v", err)
	}
	if _, err := bin.Stat("/notes.txt"); !os.IsNotExist(err) {
		t.Errorf("a normal file landed in the mount: %v", err)
	}
}

func TestNamesThatMerelyStartWithTheMountAreNotMounted(t *testing.T) {
	// /binary is not under /bin. A prefix check without the separator would
	// silently divert it, and the file would vanish from the host.
	base, bin, m := two(t)

	for _, name := range []string{"/binary", "/bindings/x", "/sbin/ls"} {
		if err := afero.WriteFile(m, name, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if _, err := base.Stat(name); err != nil {
			t.Errorf("%s missed the host: %v", name, err)
		}
		if _, err := bin.Stat(name); !os.IsNotExist(err) {
			t.Errorf("%s was diverted into the mount", name)
		}
	}
}

func TestLongestPrefixWins(t *testing.T) {
	base := afero.NewMemMapFs()
	outer := afero.NewMemMapFs()
	inner := afero.NewMemMapFs()
	m := Mount(base, map[string]afero.Fs{"/a": outer, "/a/b": inner})

	if err := afero.WriteFile(m, "/a/b/deep.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Stat("/a/b/deep.txt"); err != nil {
		t.Errorf("the deeper mount did not win: %v", err)
	}
	if _, err := outer.Stat("/a/b/deep.txt"); !os.IsNotExist(err) {
		t.Errorf("the shallower mount took it")
	}
}

func TestTheParentListingShowsTheMountPoint(t *testing.T) {
	// Without this, `ls /` would not show bin while `cd /bin` worked, which
	// reads as a filesystem that has lost a directory.
	base, bin, m := two(t)
	if err := afero.WriteFile(base, "/notes.txt", []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(bin, "/bin/ls", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	f, err := m.Open("/")
	if err != nil {
		t.Fatalf("open /: %v", err)
	}
	defer f.Close() //nolint:errcheck
	ents, err := f.Readdir(0)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var sawBin, sawNotes bool
	for _, n := range names {
		switch n {
		case "bin":
			sawBin = true
		case "notes.txt":
			sawNotes = true
		}
	}
	if !sawBin {
		t.Errorf("the mount point is missing from the listing: %v", names)
	}
	if !sawNotes {
		t.Errorf("the host's own entries are missing: %v", names)
	}
}

func TestTheMountPointReportsAsADirectory(t *testing.T) {
	_, bin, m := two(t)
	if err := afero.WriteFile(bin, "/bin/ls", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := m.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	ents, err := f.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() == "bin" && !e.IsDir() {
			t.Error("the mount point is not reported as a directory")
		}
	}
}

func TestRenameAcrossABoundaryIsRefused(t *testing.T) {
	// Refused rather than emulated with a copy: moving a file out of the
	// synthetic /bin onto the machine is not a rename, and quietly making it
	// one would hide which filesystem ended up with the file.
	_, bin, m := two(t)
	if err := afero.WriteFile(bin, "/bin/ls", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("/bin/ls", "/ls"); err == nil {
		t.Fatal("a cross-filesystem rename was accepted")
	}
	// Within one filesystem it still works.
	if err := m.Rename("/bin/ls", "/bin/ls2"); err != nil {
		t.Errorf("a rename inside the mount failed: %v", err)
	}
}

func TestReadingBackThroughTheMount(t *testing.T) {
	_, bin, m := two(t)
	if err := afero.WriteFile(bin, "/bin/ls", []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := afero.ReadFile(m, "/bin/ls")
	if err != nil || string(got) != "stub" {
		t.Fatalf("read back: %q %v", got, err)
	}
	if _, err := m.Stat("/bin/ls"); err != nil {
		t.Errorf("stat through the mount: %v", err)
	}
}
