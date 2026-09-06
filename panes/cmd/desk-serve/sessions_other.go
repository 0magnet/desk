//go:build !js && !unix

package main

import "github.com/0magnet/desk/panes/hostagent"

// Windows and plan9 have no spare signal to ask a running process a question
// with, so the listing has no trigger there and the flag that would mention it
// says nothing. The rest of --reconnect works; what is missing is only the way
// to be reminded of what it left running.
//
// A file rather than a runtime check because syscall.SIGUSR1 does not exist on
// those platforms at all — this is a compile error, not a branch — and because
// the empty name is what the warning in warnAboutHostAccess tests to decide
// whether to promise something that would not happen.
const listSessionsSignal = ""

func printSessionsOnSignal(_ *hostagent.Registry) {}
