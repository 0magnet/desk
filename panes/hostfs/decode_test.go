package hostfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/0magnet/desk/panes/hostproto"
)

// --- the response body ---
//
// A synchronous XHR may not set responseType, so a file comes back as text
// decoded with x-user-defined: every byte maps to a distinct code point, and
// masking gets it back. Read as UTF-8 instead, every file that is not valid
// UTF-8 — which is every binary file — would be corrupted, so this is the
// difference between reading a program and reading a mangled copy of one.

func TestDecodeUserDefinedRoundTripsEveryByte(t *testing.T) {
	// The encoding the browser applies: bytes under 0x80 are themselves,
	// everything above sits at U+F700 plus the byte.
	want := make([]byte, 256)
	encoded := make([]rune, 256)
	for i := 0; i < 256; i++ {
		want[i] = byte(i)
		if i < 0x80 {
			encoded[i] = rune(i)
		} else {
			encoded[i] = rune(0xF700 + i)
		}
	}
	got := decodeUserDefined(string(encoded))
	if len(got) != len(want) {
		t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d decoded to %#x, want %#x", i, got[i], want[i])
		}
	}
}

func TestDecodeUserDefinedKeepsPlainText(t *testing.T) {
	const s = "#!/bin/sh\necho hello\n"
	if got := string(decodeUserDefined(s)); got != s {
		t.Errorf("decoded %q, want %q", got, s)
	}
}

func TestDecodeUserDefinedOfNothingIsNothing(t *testing.T) {
	if got := decodeUserDefined(""); len(got) != 0 {
		t.Errorf("decoded %q from an empty body", got)
	}
}

// --- the error kinds ---
//
// afero's callers do not read error strings, they call os.IsNotExist. An error
// that carries the right words and fails that test sends a file manager down
// the "something went wrong" path when it should be offering to create the
// file, so these assert the TEST passes and not that the message reads well.

func replyBody(t *testing.T, kind, msg string) []byte {
	t.Helper()
	b, err := json.Marshal(hostproto.ErrorReply{Kind: kind, Msg: msg})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeErrorIsTestableByTheStandardHelpers(t *testing.T) {
	for _, tc := range []struct {
		kind string
		is   error
		name string
	}{
		{hostproto.ErrNotExist, fs.ErrNotExist, "not exist"},
		{hostproto.ErrExist, fs.ErrExist, "exist"},
		{hostproto.ErrPermission, fs.ErrPermission, "permission"},
	} {
		err := decodeError(replyBody(t, tc.kind, "some message"), 404)
		if err == nil {
			t.Fatalf("%s: decoded no error at all", tc.name)
		}
		if !errors.Is(err, tc.is) {
			t.Errorf("%s: errors.Is failed for %v", tc.name, err)
		}
	}

	// And through the helpers callers actually reach for, in the shape they
	// see them: wrapped in the PathError that hostfs builds.
	pathErr := func(kind string) error {
		return &os.PathError{Op: "open", Path: "/x", Err: decodeError(replyBody(t, kind, "m"), 404)}
	}
	if !os.IsNotExist(pathErr(hostproto.ErrNotExist)) {
		t.Error("os.IsNotExist did not recognize a notexist reply")
	}
	if !os.IsExist(pathErr(hostproto.ErrExist)) {
		t.Error("os.IsExist did not recognize an exist reply")
	}
	if !os.IsPermission(pathErr(hostproto.ErrPermission)) {
		t.Error("os.IsPermission did not recognize a permission reply")
	}
}

func TestKnownKindsComeBackAsTheBareSentinel(t *testing.T) {
	// Deliberate, and the reason is in decode.go: os.IsNotExist does not
	// unwrap, so a wrapped sentinel fails it — and afero itself calls
	// os.IsNotExist to decide whether to create a file. Wrapping to keep the
	// agent's message would cost more than the message is worth, since the
	// caller's PathError already carries the operation and the path.
	//
	// Pinned as a test so that "improving" the message reintroduces the bug
	// loudly instead of silently.
	if got := decodeError(replyBody(t, hostproto.ErrNotExist, "open /etc/nope: no such file"), 404); got != fs.ErrNotExist {
		t.Errorf("got %#v, want the bare fs.ErrNotExist", got)
	}
}

func TestAWrappedSentinelWouldNotHaveDone(t *testing.T) {
	// The failure this file exists for, demonstrated rather than described: a
	// %w-wrapped sentinel satisfies errors.Is and fails os.IsNotExist, so a
	// test that only checked errors.Is would have passed while afero could
	// not tell a missing file from a broken one.
	wrapped := fmt.Errorf("no such file: %w", fs.ErrNotExist)
	if !errors.Is(wrapped, fs.ErrNotExist) {
		t.Fatal("errors.Is should accept a wrapped sentinel")
	}
	pe := &os.PathError{Op: "open", Path: "/x", Err: wrapped}
	if os.IsNotExist(pe) {
		t.Fatal("os.IsNotExist accepted a wrapped sentinel; this test is out of date")
	}
	// And the real thing, in the shape the caller builds.
	real := &os.PathError{Op: "open", Path: "/x", Err: decodeError(replyBody(t, hostproto.ErrNotExist, "x"), 404)}
	if !os.IsNotExist(real) {
		t.Error("os.IsNotExist rejected what hostfs actually returns")
	}
}

func TestDecodeErrorOfAnUnknownKindIsPlain(t *testing.T) {
	// "other" must NOT satisfy any of the standard tests — a generic failure
	// reported as not-exist would have a file manager create files after a
	// disk error.
	err := decodeError(replyBody(t, hostproto.ErrOther, "disk on fire"), 400)
	if err == nil {
		t.Fatal("decoded no error")
	}
	if os.IsNotExist(err) || os.IsExist(err) || os.IsPermission(err) {
		t.Errorf("a generic failure was classified: %v", err)
	}
	if !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("the message was lost: %v", err)
	}
}

func TestDecodeErrorSurvivesABodyThatIsNotJSON(t *testing.T) {
	// An agent that is not there, a proxy in the way, a 502 from something
	// else entirely: the status is all there is, and it has to come back as an
	// error rather than a nil that reads as success.
	err := decodeError([]byte("<html>502 Bad Gateway</html>"), 502)
	if err == nil {
		t.Fatal("a non-JSON body decoded to no error, which would read as success")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("the status is not in %v", err)
	}
}

func TestDecodeErrorSurvivesAnEmptyBody(t *testing.T) {
	if err := decodeError(nil, 500); err == nil {
		t.Fatal("an empty body decoded to no error")
	}
}
