//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"
	"github.com/0magnet/websh/web"
	xterm "github.com/0magnet/xterm-go"

	"github.com/0magnet/desk"
	"github.com/0magnet/desk/panes/hostterm"
)

// registerLauncherApplets lets the shell open windows, which is the only
// launcher the demo needs: there is already a command line on the screen.
// Applet output is dropped on a write error, as in websh's shell/write.go: when
// a write to the terminal or the next stage of a pipeline fails there is
// nowhere left to report it, since a diagnostic would go to the same broken
// stream. The bare assignment is what says the choice was made rather than
// missed.
func registerLauncherApplets() {
	shell.RegisterApplet("open", "open a desk app in a window (open -l to list)",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
			if len(args) == 0 || args[0] == "-l" || args[0] == "--list" {
				for _, a := range desk.Apps() {
					_, _ = fmt.Fprintf(hc.Stdout, "  %-10s %s\n", a.Name, a.Help) //nolint:errcheck // applet output; see the note above
				}
				return 0
			}
			if _, err := desk.Launch(args[0], args[1:]...); err != nil {
				_, _ = fmt.Fprintln(hc.Stderr, "open:", err) //nolint:errcheck // applet output; see the note above
				return 1
			}
			return 0
		})

	shell.RegisterApplet("term", "open another terminal window",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
			if _, err := desk.Launch("term", args...); err != nil {
				_, _ = fmt.Fprintln(hc.Stderr, "term:", err) //nolint:errcheck // applet output; see the note above
				return 1
			}
			return 0
		})

	shell.RegisterApplet("apps", "list the windows this desk can open",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, _ []string) int {
			var b strings.Builder
			for _, a := range desk.Apps() {
				_, _ = fmt.Fprintf(&b, "  %-10s %s\n", a.Name, a.Help) //nolint:errcheck // applet output; see the note above
			}
			_, _ = fmt.Fprint(hc.Stdout, b.String()) //nolint:errcheck // applet output; see the note above
			return 0
		})
}

// hostAppletTerminal finds the terminal the applet is running in.
//
// An applet is handed pipes and a *shell.Shell, never a terminal — the right
// shape for the ordinary kind and not enough for one that has to draw. websh
// publishes the shell-to-session pairing for exactly this, so the terminal is
// reachable without the embedder keeping its own note of which pane is "the"
// shell, which is wrong the moment there are two terminals open.
func hostAppletTerminal(s *shell.Shell) *xterm.Terminal { return web.TerminalFor(s) }

// registerHostApplet adds `host`, which attaches a shell on this machine to
// THIS terminal instead of opening a window.
//
// The desktop behavior, deliberately: on a Linux machine typing ssh does not
// spawn a window, it takes over the terminal you typed it in, and when the
// remote shell exits you are back at your own prompt with the scrollback
// intact. `open host` is still there for the window version — this is the one
// that matches the machine it is running on.
//
// `host NAME` names the session, so the shell behind it outlives both this
// attachment and the window: leave it, close the tab, come back, type the same
// thing, and you are back in it with what it printed meanwhile replayed. That
// only works with --reconnect on the other end; without it the name is
// ignored and this is an ordinary shell.
func registerHostApplet() {
	shell.RegisterApplet("host", "a shell on this machine, in this terminal (host NAME to reconnect)",
		func(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
			term := hostAppletTerminal(s)
			if term == nil {
				fmt.Fprintln(hc.Stderr, "host: no terminal to attach to") //nolint:errcheck
				return 1
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}

			// Raw mode BEFORE attaching: the shell otherwise line-buffers and
			// echoes, so the remote pty would receive whole lines late and the
			// terminal would show every keystroke twice.
			if s.RawMode != nil {
				s.RawMode(true)
				defer s.RawMode(false)
			}

			// A newline BEFORE the remote's first byte. Raw mode is already on,
			// so websh did not echo the Return that ran this — the cursor is
			// still sitting after "host demo" and the remote's first output
			// would start on that line, on top of the command that asked for
			// it. A shell echoes the newline for exactly this reason.
			fmt.Fprint(hc.Stdout, "\r\n") //nolint:errcheck

			att, err := hostterm.Attach(term, name)
			if err != nil {
				fmt.Fprintf(hc.Stderr, "host: %v\n", err) //nolint:errcheck
				return 1
			}
			defer att.Close()

			// Pump raw keystrokes to the pty. websh hands them to this
			// applet's stdin while raw mode is on, Ctrl+C included, which is
			// what lets the remote program see an interrupt instead of this
			// one being killed by it.
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := hc.Stdin.Read(buf)
					if n > 0 {
						att.Send(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()

			// Either the remote shell exited, or the session was cancelled
			// from outside. Both mean the same thing here: give the terminal
			// back.
			select {
			case <-att.Done():
				// Let the last of the pty's output land before taking the
				// terminal back. The socket's close and the messages ahead of
				// it are separate JS tasks, so returning the instant Done
				// fires means websh draws its prompt and the remote's parting
				// "exit" is then written over it — which is what the two
				// shells fighting for one cursor looks like.
				time.Sleep(120 * time.Millisecond)
			case <-ctx.Done():
			}

			// A remote full-screen program may have left the cursor hidden or
			// a scroll region set, and the prompt about to be printed would
			// inherit both. Reset rather than clear: clearing would throw away
			// the session the user just had, which is the thing worth keeping.
			// Hand the terminal back in a known state. A remote full-screen
			// program may have left the cursor hidden or a scroll region set,
			// and the prompt about to be printed would inherit both. The
			// trailing newline is what puts that prompt on a line of its own
			// rather than at whatever column the remote stopped in.
			fmt.Fprint(hc.Stdout, "\x1b[?25h\x1b[r\x1b[0m\r\n") //nolint:errcheck
			return 0
		})
}
