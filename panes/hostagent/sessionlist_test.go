//go:build !js

package hostagent

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0magnet/desk/panes/hostproto"
)

// The tests for the operator's view of what is running.
//
// Two halves, and they are different kinds of test on purpose. The ones that
// drive a real shell check that the registry's bookkeeping matches what the
// sessions are actually doing — that is where a mistake would be invisible,
// because a listing is believed. The ones that build a SessionInfo by hand and
// render it check the printing, which has to be exercised at a clock and a
// buffer size no real session would reach in a test.

// events collects what a registry reported, for the tests that care about the
// order things happened in.
type events struct {
	mu   sync.Mutex
	seen []SessionEvent
	last map[SessionEvent]SessionInfo
}

func newEvents() *events {
	return &events{last: map[SessionEvent]SessionInfo{}}
}

func (e *events) notify(ev SessionEvent, s SessionInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, ev)
	e.last[ev] = s
}

func (e *events) list() []SessionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]SessionEvent(nil), e.seen...)
}

func (e *events) has(want SessionEvent) bool {
	for _, ev := range e.list() {
		if ev == want {
			return true
		}
	}
	return false
}

func (e *events) info(ev SessionEvent) SessionInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last[ev]
}

func TestTheListingShowsADetachedShell(t *testing.T) {
	// The whole point of the feature: a shell with no window on it is
	// invisible, and this is the one thing that makes it visible again.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "kilo", 0, 0)
	r := newReader(ws)
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	r.wait(t, "READY")
	ws.Close() //nolint:errcheck,gosec
	waitFor(t, "the session to detach", func() bool {
		l := reg.List()
		return len(l) == 1 && !l[0].Attached
	})

	got := reg.List()[0]
	if got.Name != "kilo" {
		t.Errorf("Name = %q, want the name the client asked for", got.Name)
	}
	if got.PID <= 0 {
		t.Errorf("PID = %d, so the listing cannot be acted on", got.PID)
	}
	if got.Started.IsZero() || got.Since.IsZero() {
		t.Errorf("no clocks on the session: started %v, since %v", got.Started, got.Since)
	}
	if got.Since.Before(got.Started) {
		t.Errorf("detached at %v, which is before it started at %v", got.Since, got.Started)
	}
	// The default idle timeout is an hour, so the deadline has to be in the
	// future and near it. A zero here would say nothing will ever reap this,
	// which is the answer that would let a forgotten shell live forever.
	if got.ReapAt.IsZero() {
		t.Fatal("a detached session reported no reap deadline")
	}
	if d := time.Until(got.ReapAt); d < 55*time.Minute || d > time.Hour {
		t.Errorf("reap deadline is %v away, want about an hour", d)
	}
	// It printed a prompt and echoed a command, so there is a transcript to
	// come back to. Zero would mean the ring is not being fed, which is the
	// same bug as an empty window on re-attach.
	if got.Buffered <= 0 {
		t.Errorf("Buffered = %d, but the shell has printed", got.Buffered)
	}
}

func TestTheListingSaysWhoIsAttached(t *testing.T) {
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "lima", 0, 0)
	defer ws.Close() //nolint:errcheck
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	got := reg.List()[0]
	if !got.Attached {
		t.Error("a session with a live socket was reported as detached")
	}
	// Nothing is counting down while somebody is looking at it, and saying
	// otherwise would put a deadline in the operator's report that no timer
	// is going to honor.
	if !got.ReapAt.IsZero() {
		t.Errorf("an attached session reported a reap deadline of %v", got.ReapAt)
	}
}

func TestAnUnnamedConnectionIsNotInTheListing(t *testing.T) {
	// The companion to the compatibility test in reconnect_test.go. A client
	// that never asked for reconnection has a shell that dies with its
	// socket, and it is not in the registry — so the listing must not claim
	// it is one of the things left running.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	newReader(ws).wait(t, "READY")

	if l := reg.List(); len(l) != 0 {
		t.Errorf("the listing has %d entries for a connection that named none: %+v", len(l), l)
	}
}

func TestTheListingPutsDetachedSessionsFirst(t *testing.T) {
	// The order is the answer to the question. "What did I leave running" is
	// about the rows nobody is looking at, and they have to be where they
	// will be read.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	gone := dialSession(t, srv, cfg, "zulu-detached", 0, 0)
	newReader(gone).wait(t, "$")
	waitFor(t, "the first session to be registered", func() bool { return reg.Len() == 1 })
	gone.Close() //nolint:errcheck,gosec

	here := dialSession(t, srv, cfg, "alpha-attached", 0, 0)
	defer here.Close() //nolint:errcheck
	newReader(here).wait(t, "$")
	waitFor(t, "both sessions to settle", func() bool {
		l := reg.List()
		return len(l) == 2 && !l[0].Attached && l[1].Attached
	})
	// Sorted by name inside each group, the attached one would come first;
	// it is second because being attached is what sorts last.
	if l := reg.List(); l[0].Name != "zulu-detached" {
		t.Errorf("the listing led with %q, not the detached session", l[0].Name)
	}
}

func TestTheListingReportsTheBufferSizeAndNotItsContents(t *testing.T) {
	// The ring holds whatever the shell printed, which is the one thing here
	// that can be a password. The report says how much there is; reading it
	// back is a different feature with a different conversation attached.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "mike", 0, 0)
	r := newReader(ws)
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo SEC''RET\n"})
	r.wait(t, "SECRET")
	ws.Close() //nolint:errcheck,gosec
	waitFor(t, "the session to detach", func() bool {
		l := reg.List()
		return len(l) == 1 && !l[0].Attached
	})

	var out strings.Builder
	PrintSessions(&out, "desk: ", reg.List())
	if strings.Contains(out.String(), "SECRET") {
		t.Errorf("the listing printed what the shell printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "mike") {
		t.Errorf("the listing does not name the session:\n%s", out.String())
	}
}

func TestTheReportedPIDIsTheShellAndKillingItEndsTheSession(t *testing.T) {
	// This is the test behind the decision NOT to build a way to kill one
	// session from the outside: the listing hands over a pid, kill(1) ends
	// the shell, and the pump notices the pty close and takes the session out
	// of the registry by itself. If that chain did not hold, the pid would be
	// a number in a table rather than a handle on anything.
	//
	// SIGKILL rather than a polite SIGTERM because the target is an
	// INTERACTIVE shell, and an interactive sh or bash ignores SIGTERM by
	// design. That is worth knowing before telling somebody to use kill(1):
	// the answer is kill -HUP or kill -9, not a bare kill.
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{})

	ws := dialSession(t, srv, cfg, "november", 0, 0)
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })
	ws.Close() //nolint:errcheck,gosec
	waitFor(t, "the session to detach", func() bool {
		l := reg.List()
		return len(l) == 1 && !l[0].Attached
	})

	pid := reg.List()[0].PID
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid, err)
	}
	if err := p.Signal(os.Kill); err != nil {
		t.Fatalf("killing the reported pid %d: %v", pid, err)
	}
	waitFor(t, "the killed session to leave the registry", func() bool { return len(reg.List()) == 0 })
}

// --- the log of what happened ---

func TestTheEventsFollowASessionThroughItsLife(t *testing.T) {
	ev := newEvents()
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{
		IdleTimeout: 250 * time.Millisecond,
		Notify:      ev.notify,
	})

	ws := dialSession(t, srv, cfg, "oscar", 0, 0)
	newReader(ws).wait(t, "$")
	waitFor(t, "the created event", func() bool { return ev.has(SessionCreated) })
	ws.Close() //nolint:errcheck,gosec

	waitFor(t, "the detached event", func() bool { return ev.has(SessionDetached) })
	waitFor(t, "the reaped event", func() bool { return ev.has(SessionReaped) })
	waitFor(t, "the session to leave the registry", func() bool { return reg.Len() == 0 })

	// Created, detached, reaped, in that order and once each: the log is
	// only useful if it is an account rather than a set of things that
	// happened at some point.
	got := ev.list()
	want := []SessionEvent{SessionCreated, SessionDetached, SessionReaped}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	// The detach event carries the deadline, which is the useful half of
	// being told a shell was just left running.
	if ev.info(SessionDetached).ReapAt.IsZero() {
		t.Error("the detach event did not say when the session would be reaped")
	}
	if ev.info(SessionCreated).PID <= 0 {
		t.Error("the created event did not carry a pid")
	}
}

func TestComingBackIsReportedAsAttachedAndNotAsCreated(t *testing.T) {
	// The two are different news — a new shell on the machine, versus
	// somebody returning to one that was already there — and conflating them
	// would make the log unable to answer "how many shells did I start".
	ev := newEvents()
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{Notify: ev.notify})

	ws := dialSession(t, srv, cfg, "papa", 0, 0)
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })
	ws.Close() //nolint:errcheck,gosec
	waitFor(t, "the detached event", func() bool { return ev.has(SessionDetached) })

	ws2 := dialSession(t, srv, cfg, "papa", 0, 0)
	defer ws2.Close() //nolint:errcheck
	newReader(ws2).wait(t, "$")
	waitFor(t, "the attached event", func() bool { return ev.has(SessionAttached) })

	created := 0
	for _, e := range ev.list() {
		if e == SessionCreated {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d shells reported as started, but only one was", created)
	}
}

func TestAShellThatExitsIsReportedAsHavingExited(t *testing.T) {
	// Three different endings, three different events, because "the timeout
	// got it" and "somebody typed exit" are not the same story about a
	// machine.
	ev := newEvents()
	srv, cfg, _ := reconnectAgent(t, RegistryConfig{Notify: ev.notify})

	ws := dialSession(t, srv, cfg, "quebec", 0, 0)
	defer ws.Close() //nolint:errcheck
	newReader(ws).wait(t, "$")
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "exit\n"})

	waitFor(t, "the exited event", func() bool { return ev.has(SessionExited) })
	if ev.has(SessionReaped) {
		t.Error("a shell that exited was also reported as reaped")
	}
}

func TestStoppingTheServerReportsEverySessionOnce(t *testing.T) {
	ev := newEvents()
	srv, cfg, reg := reconnectAgent(t, RegistryConfig{Notify: ev.notify})

	ws := dialSession(t, srv, cfg, "romeo", 0, 0)
	defer ws.Close() //nolint:errcheck
	newReader(ws).wait(t, "$")
	waitFor(t, "the session to be registered", func() bool { return reg.Len() == 1 })

	reg.Close()
	reg.Close() // the second one must not report an ending that did not happen
	stopped := 0
	for _, e := range ev.list() {
		if e == SessionStopped {
			stopped++
		}
	}
	if stopped != 1 {
		t.Errorf("one session ending was reported %d times", stopped)
	}
}

// --- the rendering, at clocks and sizes no test can wait for ---

func TestPrintSessionsSaysSoWhenThereIsNothing(t *testing.T) {
	// The common case and the one that has to be unmistakable: an operator
	// pressing a key to check they left nothing behind needs an answer, not
	// an empty table that looks like the signal did not arrive.
	var out strings.Builder
	PrintSessions(&out, "desk: ", nil)
	if !strings.Contains(out.String(), "no host shells") {
		t.Errorf("printed %q for an empty registry", out.String())
	}
}

func TestPrintSessionsRendersTheColumns(t *testing.T) {
	now := time.Now()
	ss := []SessionInfo{{
		Name:     "build",
		PID:      4242,
		Started:  now.Add(-3 * time.Hour),
		Since:    now.Add(-90 * time.Second),
		ReapAt:   now.Add(58*time.Minute + 30*time.Second),
		Buffered: 256 << 10,
	}, {
		Name:     "watching",
		PID:      4243,
		Attached: true,
		Started:  now.Add(-time.Minute),
		Since:    now.Add(-time.Minute),
		Buffered: 300,
	}}
	var out strings.Builder
	PrintSessions(&out, "desk: ", ss)
	got := out.String()
	for _, want := range []string{
		"2 host shell(s), 1 of them detached",
		"build", "4242", "detached", "1m30s", "58m30s", "256 KiB",
		"watching", "attached", "300 B",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the listing is missing %q:\n%s", want, got)
		}
	}
	// Every line carries the prefix, including the ones tabwriter padded,
	// so that the report is greppable out of a scrollback.
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasPrefix(line, "desk: ") {
			t.Errorf("line without the prefix: %q", line)
		}
	}
}

func TestPrintSessionsDistinguishesNeverFromNotYet(t *testing.T) {
	// A negative IdleTimeout means nothing reaps these, which is tmux's
	// behavior and a very different thing to tell somebody than "in an
	// hour". An attached session has no deadline either, but for the
	// opposite reason — the clock has not started — so it must not be
	// reported as one that will never be reaped.
	now := time.Now()
	var out strings.Builder
	PrintSessions(&out, "", []SessionInfo{
		{Name: "forever", PID: 1, Since: now},
		{Name: "watched", PID: 2, Attached: true, Since: now},
	})
	got := out.String()
	if !strings.Contains(got, "never") {
		t.Errorf("a session nothing will reap was not reported as such:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "watched") && strings.Contains(line, "never") {
			t.Errorf("an attached session was reported as never being reaped: %q", line)
		}
	}
}

func TestAHostileSessionNameCannotRewriteTheListing(t *testing.T) {
	// The name is up to 128 arbitrary bytes chosen by whoever holds the
	// token, and the destination is a terminal. Printed raw, a name
	// containing carriage returns and cursor movement can overwrite the rows
	// around it — so the one session you were looking for is the one the
	// report does not show. A tool whose whole job is to reveal something
	// must not be steerable by the thing it reveals.
	var out strings.Builder
	PrintSessions(&out, "desk: ", []SessionInfo{
		{Name: "\x1b[2K\rinnocent", PID: 7, Since: time.Now()},
	})
	got := out.String()
	if strings.ContainsAny(got, "\x1b\r") {
		t.Errorf("the listing passed control bytes through to the terminal: %q", got)
	}
	if !strings.Contains(got, `\x1b`) {
		t.Errorf("the quoted name is not visible in the listing: %q", got)
	}
}

func TestAnOrdinaryNameIsPrintedPlainly(t *testing.T) {
	// The quoting has to be the exception, or every row is quoted and the
	// one that matters stops standing out.
	if got := printName("build-2"); got != "build-2" {
		t.Errorf("printName = %q, want it untouched", got)
	}
	if got := printName(""); got != `""` {
		t.Errorf("printName of an empty name = %q, want something visible", got)
	}
	if got := printName("naïve"); !strings.HasPrefix(got, `"`) {
		t.Errorf("printName = %q, want a non-ASCII name quoted", got)
	}
}

func TestPrintEventSaysWhatHappenedAndWhatItMeans(t *testing.T) {
	now := time.Now()
	s := SessionInfo{Name: "build", PID: 4242, Since: now.Add(-time.Hour), ReapAt: now.Add(30 * time.Minute)}

	var out strings.Builder
	PrintEvent(&out, "desk: ", SessionDetached, s)
	got := out.String()
	// The detach line is the one that has to carry its consequences: the
	// shell is still there, nothing shows it, and here is the deadline.
	for _, want := range []string{"build", "4242", "detached", "still running", "30m0s"} {
		if !strings.Contains(got, want) {
			t.Errorf("the detach line is missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("an event was not one line: %q", got)
	}

	out.Reset()
	PrintEvent(&out, "desk: ", SessionReaped, s)
	if !strings.Contains(out.String(), "1h0m0s") {
		t.Errorf("the reap line does not say how long it was idle: %q", out.String())
	}

	out.Reset()
	PrintEvent(&out, "desk: ", SessionDetached, SessionInfo{Name: "forever", PID: 9, Since: now})
	if !strings.Contains(out.String(), "nothing will reap it") {
		t.Errorf("a detach with no deadline did not say so: %q", out.String())
	}
}

func TestShortDurationAndShortBytesReadLikeSomethingSaidOutLoud(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0s"}, // a deadline that passed while this was formatting
		{1500 * time.Millisecond, "2s"},
		{90 * time.Second, "1m30s"},
		{2*time.Hour + 14*time.Minute + 53*time.Second, "2h15m0s"},
	} {
		if got := shortDuration(c.in); got != c.want {
			t.Errorf("shortDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		in   int
		want string
	}{{0, "0 B"}, {1023, "1023 B"}, {1024, "1 KiB"}, {256 << 10, "256 KiB"}, {3 << 20, "3 MiB"}} {
		if got := shortBytes(c.in); got != c.want {
			t.Errorf("shortBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
