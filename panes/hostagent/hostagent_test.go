//go:build !js

package hostagent

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/0magnet/desk/panes/hostproto"
)

// sh is used instead of $SHELL throughout: these tests assert on what the shell
// prints, and a machine whose owner runs fish would fail them for no reason.
const sh = "/bin/sh"

// reader accumulates everything read and lets a test wait for a substring.
//
// Waiting for a substring rather than reading a fixed number of bytes is not
// laziness — a pty delivers whatever the kernel had at the moment of the read,
// so the prompt, the echo of the input and the output of the command arrive in
// an order and a grouping that is not stable between runs or between machines.
type reader struct {
	mu   sync.Mutex
	buf  strings.Builder
	done chan struct{}
}

func newReader(r io.Reader) *reader {
	rd := &reader{done: make(chan struct{})}
	go func() {
		defer close(rd.done)
		b := make([]byte, 4096)
		for {
			n, err := r.Read(b)
			if n > 0 {
				rd.mu.Lock()
				rd.buf.Write(b[:n])
				rd.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return rd
}

func (r *reader) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func (r *reader) wait(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := r.text(); strings.Contains(s, want) {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never saw %q; got:\n%s", want, r.text())
	return ""
}

func TestSessionRunsACommand(t *testing.T) {
	s, err := Start(SessionConfig{Shell: sh})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close() //nolint:errcheck

	r := newReader(s)
	// The quotes are load-bearing. A pty echoes what is typed, so a marker
	// written plainly would appear in the transcript whether or not the shell
	// ever ran it; split by quotes, the echo says RE''ADY and only the
	// command's own output can say READY.
	if _, err := s.Write([]byte("echo RE''ADY\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := r.wait(t, "READY")
	if !strings.Contains(got, "RE''ADY") {
		t.Errorf("the input was not echoed back, so this may not be a pty at all:\n%s", got)
	}
}

func TestResizeReachesTheShell(t *testing.T) {
	s, err := Start(SessionConfig{Shell: sh, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close() //nolint:errcheck

	r := newReader(s)
	if err := s.Resize(132, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	// stty asks the kernel, not the shell, so this checks that the resize
	// actually became a TIOCSWINSZ on the pty rather than a field somewhere.
	if _, err := s.Write([]byte("stty size\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := r.wait(t, "40 132")
	if strings.Contains(got, "24 80") {
		t.Errorf("the old size was still reported:\n%s", got)
	}
}

func TestInitialSizeIsSetBeforeTheFirstPrompt(t *testing.T) {
	// Starting at the right size and starting at 80x24 then correcting look
	// identical a second later and completely different to a shell drawing
	// its first prompt, which is why the size is a field on SessionConfig
	// rather than something the transport does after connecting.
	s, err := Start(SessionConfig{Shell: sh, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close() //nolint:errcheck

	r := newReader(s)
	if _, err := s.Write([]byte("stty size\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	r.wait(t, "30 100")
}

func TestResizeRefusesAZeroSizedWindow(t *testing.T) {
	s, err := Start(SessionConfig{Shell: sh})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close() //nolint:errcheck

	for _, tc := range [][2]int{{0, 24}, {80, 0}, {-1, -1}} {
		if err := s.Resize(tc[0], tc[1]); err == nil {
			t.Errorf("Resize(%d, %d) was accepted", tc[0], tc[1])
		}
	}
}

func TestSessionEndsWhenTheShellExits(t *testing.T) {
	s, err := Start(SessionConfig{Shell: sh})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close() //nolint:errcheck

	r := newReader(s)
	if _, err := s.Write([]byte("exit\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the shell exited but Done was never closed")
	}
	// The read loop must have finished too: a pty master returns EIO once
	// the last slave is gone, and if that were surfaced as an error rather
	// than EOF every clean logout would be reported as a fault.
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("reading did not end after the shell exited")
	}
}

func TestCloseKillsAShellThatIgnoresHangup(t *testing.T) {
	// The whole reason Close does more than shut the file: a shell that
	// traps HUP would otherwise outlive the window that owned it.
	s, err := Start(SessionConfig{Shell: sh})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	newReader(s)
	if _, err := s.Write([]byte("trap '' HUP\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }() //nolint:errcheck
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on a shell ignoring SIGHUP")
	}
}

// --- the transport ---

func agent(t *testing.T) (*httptest.Server, Config) {
	t.Helper()
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	cfg := Config{Token: tok, Session: SessionConfig{Shell: sh}}
	srv := httptest.NewServer(nil)
	cfg.Origins = []string{srv.URL}
	srv.Config.Handler = cfg.Handler()
	t.Cleanup(srv.Close)
	return srv, cfg
}

func wsURL(s *httptest.Server, token string) string {
	u := "ws" + strings.TrimPrefix(s.URL, "http") + hostproto.Path
	if token != "" {
		u += "?" + hostproto.TokenParam + "=" + token
	}
	return u
}

func dial(t *testing.T, s *httptest.Server, token, origin string) (*websocket.Conn, error) {
	t.Helper()
	return websocket.Dial(wsURL(s, token), "", origin)
}

func TestTokenIsRequired(t *testing.T) {
	srv, cfg := agent(t)
	for _, tc := range []struct{ name, token string }{
		{"none", ""},
		{"wrong", "00000000000000000000000000000000"},
		{"prefix of the real one", cfg.Token[:8]},
	} {
		if c, err := dial(t, srv, tc.token, srv.URL); err == nil {
			c.Close() //nolint:errcheck,gosec
			t.Errorf("%s: connected without a valid token", tc.name)
		}
	}
}

func TestAnUnconfiguredAgentRefusesEverything(t *testing.T) {
	// A zero Config must be shut, not open. This is the failure that would
	// matter most and the one a default-struct mistake would cause.
	var cfg Config
	srv := httptest.NewServer(cfg.Handler())
	defer srv.Close()
	if c, err := dial(t, srv, "", srv.URL); err == nil {
		c.Close() //nolint:errcheck,gosec
		t.Fatal("a zero Config served a shell")
	}
}

func TestForeignOriginIsRefused(t *testing.T) {
	srv, cfg := agent(t)
	// The token is correct here. This is the case that matters: a page you
	// visited knows or guesses the token but cannot change its own Origin.
	for _, origin := range []string{"http://evil.example", "null", ""} {
		if c, err := dial(t, srv, cfg.Token, origin); err == nil {
			c.Close() //nolint:errcheck,gosec
			t.Errorf("origin %q was allowed", origin)
		}
	}
}

func TestShellOverTheSocket(t *testing.T) {
	srv, cfg := agent(t)
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo RE''ADY\n"})
	r := newReader(ws)
	r.wait(t, "READY")
}

func TestResizeOverTheSocket(t *testing.T) {
	srv, cfg := agent(t)
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	send(t, ws, hostproto.Msg{T: hostproto.TypeResize, C: 132, R: 40})
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "stty size\n"})
	r := newReader(ws)
	r.wait(t, "40 132")
}

func TestAbsurdResizeIsIgnoredAndTheShellSurvives(t *testing.T) {
	srv, cfg := agent(t)
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	// 65536 columns is zero columns after the uint16 the ioctl needs, which
	// is the specific reason there is a bound rather than a cast.
	send(t, ws, hostproto.Msg{T: hostproto.TypeResize, C: 65536, R: 65536})
	send(t, ws, hostproto.Msg{T: hostproto.TypeResize, C: -1, R: -1})
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo ALI''VE\n"})
	r := newReader(ws)
	r.wait(t, "ALIVE")
}

func TestUnreadableFramesDoNotDropTheShell(t *testing.T) {
	srv, cfg := agent(t)
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	if err := websocket.Message.Send(ws, "{not json"); err != nil {
		t.Fatalf("send: %v", err)
	}
	send(t, ws, hostproto.Msg{T: "an-invented-message-type"})
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo ALI''VE\n"})
	r := newReader(ws)
	r.wait(t, "ALIVE")
}

func TestBinaryInputIsDecoded(t *testing.T) {
	srv, cfg := agent(t)
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	// The branch that exists for mouse reports, which are bytes and not
	// characters. Exercised with something whose arrival is observable.
	send(t, ws, hostproto.Msg{
		T: hostproto.TypeBinary,
		D: base64.StdEncoding.EncodeToString([]byte("echo B''IN\n")),
	})
	r := newReader(ws)
	r.wait(t, "BIN")
}

func TestUndecodableBinaryDoesNotDropTheShell(t *testing.T) {
	srv, cfg := agent(t)
	ws, err := dial(t, srv, cfg.Token, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	send(t, ws, hostproto.Msg{T: hostproto.TypeBinary, D: "not!valid!base64"})
	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "echo ALI''VE\n"})
	r := newReader(ws)
	r.wait(t, "ALIVE")
}

func TestGridComesFromTheHandshake(t *testing.T) {
	// The size has to be known before the shell exists, because a shell
	// reads it once — when it draws its first prompt. This is the same
	// property TestInitialSizeIsSetBeforeTheFirstPrompt asserts on the
	// session, checked through the transport that has to carry it.
	srv, cfg := agent(t)
	u := wsURL(srv, cfg.Token) + "&" + hostproto.ColsParam + "=120&" + hostproto.RowsParam + "=45"
	ws, err := websocket.Dial(u, "", srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "stty size\n"})
	r := newReader(ws)
	got := r.wait(t, "45 120")
	if strings.Contains(got, "24 80") {
		t.Errorf("the default grid was used instead of the requested one:\n%s", got)
	}
}

func TestAbsurdGridOnTheHandshakeFallsBackToTheDefault(t *testing.T) {
	srv, cfg := agent(t)
	u := wsURL(srv, cfg.Token) + "&" + hostproto.ColsParam + "=99999&" + hostproto.RowsParam + "=-3"
	ws, err := websocket.Dial(u, "", srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	send(t, ws, hostproto.Msg{T: hostproto.TypeInput, D: "stty size\n"})
	r := newReader(ws)
	r.wait(t, "24 80")
}

func send(t *testing.T, ws *websocket.Conn, m hostproto.Msg) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := websocket.Message.Send(ws, string(b)); err != nil {
		t.Fatalf("send: %v", err)
	}
}
