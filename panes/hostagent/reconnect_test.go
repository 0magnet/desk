//go:build !js

package hostagent

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/0magnet/desk/panes/hostproto"
)

// The reconnection tests, none of which need a browser.
//
// That is the same property the rest of this package's tests have and it is
// worth keeping deliberately: the client half is js/wasm and cannot be run
// here at all, so anything that could only be checked through a real browser
// is in practice not checked. Everything below drives the agent through the
// same WebSocket a pane would, or through the registry directly where the
// point being tested is not on the wire.

// reconnectAgent is agent() with a registry attached.
func reconnectAgent(t *testing.T, rc RegistryConfig) (*httptest.Server, Config, *Registry) {
	t.Helper()
	srv, cfg := agent(t)
	reg := NewRegistry(rc)
	t.Cleanup(reg.Close)
	cfg.Sessions = reg
	srv.Config.Handler = cfg.Handler()
	return srv, cfg, reg
}

// dialSession connects naming a session, optionally at a given grid.
func dialSession(t *testing.T, srv *httptest.Server, cfg Config, sid string, cols, rows int) *websocket.Conn {
	t.Helper()
	u := wsURL(srv, cfg.Token) + "&" + hostproto.SessionParam + "=" + sid
	if cols > 0 && rows > 0 {
		u += fmt.Sprintf("&%s=%d&%s=%d", hostproto.ColsParam, cols, hostproto.RowsParam, rows)
	}
	ws, err := websocket.Dial(u, "", srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return ws
}

// waitFor polls until cond holds, which is how everything here waits.
//
// Sleeping a fixed amount instead is what the first version of these tests did
// and it fails on a loaded machine for reasons that have nothing to do with
// the code: a shell being forked, a pty being drained and a timer firing are
// three different clocks.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestASessionWithoutAnIDStillDiesWithItsSocket(t *testing.T) {
	// The compatibility test, and the one that would matter most if it
	// broke: mounting a registry must not change what a client that never
	// asked for reconnection gets.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	newReader(ws).wait(t, "READY")
	if n := reg.Len(); n != 0 {
		t.Fatalf("an unnamed connection registered %d sessions", n)
	}
	ws.Close() //nolint:errcheck,gosec
	// Nothing to observe directly — the pty is gone with the socket — but
	// the registry staying empty is what says no session was left behind.
	time.Sleep(200 * time.Millisecond)
	if n := reg.Len(); n != 0 {
		t.Fatalf("closing an unnamed connection left %d sessions", n)
	}
}

func TestTheSameShellComesBack(t *testing.T) {
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "alpha", 0, 0)
	r := newReader(ws)
	// A shell variable is the proof that this is the same PROCESS and not
	// merely a session that replays the right transcript: nothing but the
	// original shell knows what MARK was set to.
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "MARK=hel''lo\n"})
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	r.wait(t, "READY")
	ws.Close() //nolint:errcheck,gosec

	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	ws2 := dialSession(t, srv, cfg, "alpha", 0, 0)
	defer ws2.Close() //nolint:errcheck
	r2 := newReader(ws2)
	send(t, ws2, hostproto.Msg{T: hostproto.TypeInput, D: "echo \"$MARK\"\n"})
	got := r2.wait(t, "hello")
	// The echo of the input says hel''lo, so a bare "hello" can only have
	// come from the shell expanding a variable it still had.
	if !strings.Contains(got, "hel''lo") {
		t.Errorf("the replay did not include the earlier transcript:\n%s", got)
	}
}

func TestOutputProducedWhileDetachedIsKeptAndReplayed(t *testing.T) {
	// The reason the session owns a read loop of its own. A pty master has a
	// kernel buffer of a few tens of kilobytes and a shell whose output
	// nobody drains blocks in write(2); a background job running while the
	// window is shut is exactly the case this feature is for, and it is the
	// case that silently does not work if the pty is merely left open.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "bravo", 0, 0)
	r := newReader(ws)
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "(sleep 1; echo DE''TACHED) &\n"})
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	r.wait(t, "READY")
	ws.Close() //nolint:errcheck,gosec

	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })
	time.Sleep(2 * time.Second) // long enough for the background job to finish

	ws2 := dialSession(t, srv, cfg, "bravo", 0, 0)
	defer ws2.Close() //nolint:errcheck
	newReader(ws2).wait(t, "DETACHED")
}

func TestDifferentNamesAreDifferentShells(t *testing.T) {
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	a := dialSession(t, srv, cfg, "one", 0, 0)
	defer a.Close() //nolint:errcheck
	ra := newReader(a)
	send(t, a, hostproto.Msg{T: hostproto.TypeInput, D: "MARK=fir''st\n"})
	send(t, a, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	ra.wait(t, "READY")

	b := dialSession(t, srv, cfg, "two", 0, 0)
	defer b.Close() //nolint:errcheck
	rb := newReader(b)
	send(t, b, hostproto.Msg{T: hostproto.TypeInput, D: "echo \"[$MARK]\"\n"})
	// An empty expansion. If the two names shared a shell this would say
	// [first], and the brackets are what make "nothing" observable.
	rb.wait(t, "[]")
	if reg.Len() != 2 {
		t.Errorf("two names produced %d sessions", reg.Len())
	}
}

func TestASecondClientTakesTheSessionOver(t *testing.T) {
	// Takeover rather than refusal, for the reason written at managed.attach:
	// the usual cause of a second attachment is one person whose previous
	// socket is dead and has not been noticed yet, and refusing would lock
	// them out of their own shell for as long as TCP takes to give up.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	first := dialSession(t, srv, cfg, "charlie", 0, 0)
	defer first.Close() //nolint:errcheck
	r1 := newReader(first)
	send(t, first, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	r1.wait(t, "READY")

	second := dialSession(t, srv, cfg, "charlie", 0, 0)
	defer second.Close() //nolint:errcheck

	// The displaced client's socket is closed by the agent, which is what
	// ends its read loop.
	select {
	case <-r1.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the first client was not displaced")
	}

	r2 := newReader(second)
	send(t, second, hostproto.Msg{T: hostproto.TypeInput, D: "echo TAKE''N\n"})
	r2.wait(t, "TAKEN")
	if reg.Len() != 1 {
		t.Errorf("takeover produced %d sessions, not one", reg.Len())
	}
}

func TestReattachingResizesTheShell(t *testing.T) {
	// A session found rather than started has the grid of the window that
	// left it, and the window that came back may be a different size.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "delta", 100, 30)
	r := newReader(ws)
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "stty size\n"})
	r.wait(t, "30 100")
	ws.Close() //nolint:errcheck,gosec
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	ws2 := dialSession(t, srv, cfg, "delta", 132, 40)
	defer ws2.Close() //nolint:errcheck
	r2 := newReader(ws2)
	send(t, ws2, hostproto.Msg{T: hostproto.TypeInput, D: "stty size\n"})
	r2.wait(t, "40 132")
}

func TestAnIdleSessionIsReaped(t *testing.T) {
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{IdleTimeout: 250 * time.Millisecond})

	ws := dialSession(t, srv, cfg, "echo", 0, 0)
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })
	ws.Close() //nolint:errcheck,gosec

	waitFor(t, "the idle session to be reaped", func() bool { return reg.Len() == 0 })
}

func TestReattachingCancelsTheIdleClock(t *testing.T) {
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{IdleTimeout: 400 * time.Millisecond})

	ws := dialSession(t, srv, cfg, "foxtrot", 0, 0)
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })
	ws.Close() //nolint:errcheck,gosec

	time.Sleep(150 * time.Millisecond)
	ws2 := dialSession(t, srv, cfg, "foxtrot", 0, 0)
	defer ws2.Close() //nolint:errcheck
	r := newReader(ws2)
	// Well past the timeout measured from the first disconnect. If attaching
	// did not stop the timer this shell is already dead.
	time.Sleep(600 * time.Millisecond)
	send(t, ws2, hostproto.Msg{T: hostproto.TypeInput, D: "echo ALI''VE\n"})
	r.wait(t, "ALIVE")
}

func TestAShellThatExitsIsNotLeftBehind(t *testing.T) {
	// `exit` at a prompt has always meant the shell is gone. A registry entry
	// that outlived it would hand the next window a dead pty and no way to
	// tell that is what happened.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "golf", 0, 0)
	defer ws.Close() //nolint:errcheck
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "exit\n"})
	waitFor(t, "the exited session to be forgotten", func() bool { return reg.Len() == 0 })
}

func TestTheSessionCapIsEnforced(t *testing.T) {
	// Not tidiness. Before sessions outlived their sockets the number of live
	// shells was bounded by the number of open sockets; a caller holding the
	// token can otherwise name a new session per request and leave a shell
	// behind every time.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{MaxSessions: 1})

	ws := dialSession(t, srv, cfg, "hotel", 0, 0)
	defer ws.Close() //nolint:errcheck
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	over := dialSession(t, srv, cfg, "india", 0, 0)
	defer over.Close() //nolint:errcheck
	newReader(over).wait(t, "too many detached sessions")
	if reg.Len() != 1 {
		t.Errorf("the cap was exceeded: %d sessions", reg.Len())
	}
}

func TestAnAbsurdSessionNameIsRefused(t *testing.T) {
	srv, cfg, _ := reconnectAgent(t, RegistryConfig{})
	ws := dialSession(t, srv, cfg, strings.Repeat("x", maxSessionIDLen+1), 0, 0)
	defer ws.Close() //nolint:errcheck
	newReader(ws).wait(t, "session id is too long")
}

func TestClosingTheRegistryEndsEverySession(t *testing.T) {
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "juliett", 0, 0)
	defer ws.Close() //nolint:errcheck
	r := newReader(ws)
	r.wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	reg.Close()
	if reg.Len() != 0 {
		t.Errorf("Close left %d sessions", reg.Len())
	}
	// The attached client's socket is closed too, so a window that was open
	// when the server shut down says so rather than sitting there.
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close left an attached client hanging")
	}
}

// --- addressing, which is where a mistake would be quiet ---

func TestSessionsAreBoundToTheTokenThatCreatedThem(t *testing.T) {
	// The security property that has to hold: a session created under one
	// token is not reachable under another. It is not "refused" — it is not
	// addressable, because the token is an input to the key. Today the agent
	// has one token per run, so this is a guard for a future in which it does
	// not, which is exactly when it would be too late to add.
	reg := NewRegistry(RegistryConfig{})
	defer reg.Close()

	a, err := reg.Attach("token-a", "shared-name", SessionConfig{Shell: sh}, io.Discard, func() {})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Detach()
	b, err := reg.Attach("token-b", "shared-name", SessionConfig{Shell: sh}, io.Discard, func() {})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer b.Detach()

	if a.m == b.m {
		t.Fatal("two tokens reached the same session under one name")
	}
	if reg.Len() != 2 {
		t.Fatalf("expected two independent sessions, got %d", reg.Len())
	}
}

func TestTheSameTokenAndNameReachTheSameSession(t *testing.T) {
	reg := NewRegistry(RegistryConfig{})
	defer reg.Close()

	a, err := reg.Attach("token", "name", SessionConfig{Shell: sh}, io.Discard, func() {})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !a.Created {
		t.Error("the first attach did not report creating the session")
	}
	b, err := reg.Attach("token", "name", SessionConfig{Shell: sh}, io.Discard, func() {})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer b.Detach()
	if b.Created {
		t.Error("the second attach reported creating a session")
	}
	if a.m != b.m {
		t.Fatal("the same token and name reached different sessions")
	}
}

func TestTheKeyIsNotAmbiguousAcrossTheJoin(t *testing.T) {
	// Without the length prefix, token "ab" with name "c" hashes the same
	// bytes as token "a" with name "bc". An ambiguity in a key derivation is
	// a way to reach a session that was not yours.
	reg := NewRegistry(RegistryConfig{})
	defer reg.Close()
	if reg.key("ab", "c") == reg.key("a", "bc") {
		t.Fatal("the token and the name run together in the key")
	}
}

func TestTheKeyDoesNotLeakTheName(t *testing.T) {
	// The client's name is not the key, which is the property that lets a
	// pane pick something dull without creating a session anyone can walk up
	// to. Two registries with the same inputs must disagree, because the
	// secret is what does the work.
	a, b := NewRegistry(RegistryConfig{}), NewRegistry(RegistryConfig{})
	defer a.Close()
	defer b.Close()
	if a.key("token", "1") == b.key("token", "1") {
		t.Fatal("the key does not depend on the per-run secret")
	}
	if strings.Contains(a.key("token", "a-very-distinctive-name"), "a-very-distinctive-name") {
		t.Fatal("the key contains the name it was derived from")
	}
}

// --- the ring buffer ---

func TestRingKeepsEverythingUntilItWraps(t *testing.T) {
	r := newRing(16)
	r.write([]byte("hello"))
	r.write([]byte(" world"))
	if got := string(r.replay()); got != "hello world" {
		t.Errorf("replay = %q", got)
	}
}

func TestRingKeepsTheTailOnceItWraps(t *testing.T) {
	r := newRing(8)
	r.write([]byte("abcdef"))
	r.write([]byte("ghijkl"))
	if got := string(r.replay()); got != "efghijkl" {
		t.Errorf("replay = %q, want the last eight bytes", got)
	}
}

func TestRingHandlesAChunkBiggerThanItself(t *testing.T) {
	// A single pty read can be 32 KiB, which is larger than a small buffer;
	// wrapping around several times to the same answer would be a slow way to
	// arrive at the tail.
	r := newRing(4)
	r.write([]byte("0123456789"))
	if got := string(r.replay()); got != "6789" {
		t.Errorf("replay = %q", got)
	}
}

func TestRingResynchronizesAfterWrapping(t *testing.T) {
	// The cut can land inside an escape sequence, and a terminal handed the
	// tail of a CSI eats the printable text that follows it as parameters.
	// Skipping to the first ESC puts the parser back in the ground state.
	r := newRing(8)
	r.write([]byte("AB\x1bCDEFGH"))
	if got := string(r.replay()); got != "\x1bCDEFGH" {
		t.Errorf("replay = %q, want it to start at the escape", got)
	}
}

func TestRingDoesNotResynchronizeBeforeItWraps(t *testing.T) {
	// Nothing was cut, so there is nothing to resynchronize to and skipping
	// would throw away the start of the session for no reason.
	r := newRing(64)
	r.write([]byte("AB\x1bCD"))
	if got := string(r.replay()); got != "AB\x1bCD" {
		t.Errorf("replay = %q", got)
	}
}

func TestRingWithReplayDisabledKeepsNothing(t *testing.T) {
	r := newRing(-1)
	r.write([]byte("anything"))
	if got := r.replay(); got != nil {
		t.Errorf("replay = %q, want nothing", got)
	}
}
