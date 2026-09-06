//go:build !js && unix

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/0magnet/desk/panes/hostagent"
)

// listSessionsSignal names the signal that prints the session listing, for the
// messages that have to tell somebody how to ask. Empty on the platforms that
// have no such signal; see sessions_other.go.
const listSessionsSignal = "USR1"

// printSessionsOnSignal makes SIGUSR1 print what is running to this terminal.
//
// # Why a signal, and why this signal
//
// The operator has a problem the page cannot be given the answer to: with
// --reconnect on, a shell they left behind is invisible until they happen to
// remember its name, and hostagent deliberately has no way to ask over the
// wire what exists — the reasoning is in hostagent/sessionlist.go and it is
// about who can ask rather than about what the answer contains. What is left
// is a channel only the person who started the process can use, and on a unix
// machine that is a signal and the terminal it prints to. No listener, no
// path, no token, nothing a browser can address, and nothing new to get wrong:
// the ability to signal this process is the ability to kill it, which whoever
// started it already had.
//
// USR1 because it is one of the two signals defined to have no meaning of
// their own, so claiming it cannot collide with something the runtime or a
// library is using. The alternatives were considered and lost:
//
//   - SIGINFO is the right answer and does not exist here. On a BSD it is
//     bound to ^T, so the operator asks by pressing a key in the terminal
//     they are already looking at rather than by finding a pid; Linux has no
//     such signal and Go does not define one, and a feature that works on one
//     of the two machines this runs on is a feature that gets misremembered.
//   - SIGQUIT already means something violent — the runtime dumps every
//     goroutine and aborts — and taking it would be taking a debugging tool
//     away from whoever needs it next.
//   - Reading a command off stdin needs stdin, and this process is routinely
//     started in the background or from a shell that owns the terminal; a
//     server that eats keystrokes it was not given is worse than one that
//     cannot be asked questions.
//
// The default disposition of SIGUSR1 is to KILL THE PROCESS, which is why
// installing this handler is a change in behavior and not merely an addition,
// and why it is installed only on the reconnect path — the same scoping, for
// the same reason, as reapSessionsOnSignal: without a registry there are no
// detached shells to list, and the flag being off should leave the signal
// meaning exactly what it meant before.
//
// The channel is buffered at one and the handler loops forever. Both halves
// matter: signal.Notify drops rather than blocks when the buffer is full, so a
// burst of `kill -USR1` collapses into one listing instead of queueing a
// hundred, and a handler that returned after the first signal would answer the
// question once and then be a kill switch again for the rest of the run.
func printSessionsOnSignal(reg *hostagent.Registry) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	go func() {
		for range c {
			// Listed before taking the print lock, because List waits on
			// each session's own mutex and one slow session must not be
			// able to hold up an unrelated event line.
			list := reg.List()
			hostPrint.Lock()
			hostagent.PrintSessions(os.Stdout, "desk: ", list)
			hostPrint.Unlock()
		}
	}()
}
