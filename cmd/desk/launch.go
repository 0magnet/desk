//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"

	"github.com/0magnet/desk"
)

// registerLauncherApplets lets the shell open windows, which is the only
// launcher the demo needs: there is already a command line on the screen.
func registerLauncherApplets() {
	shell.RegisterApplet("open", "open a desk app in a window (open -l to list)",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
			if len(args) == 0 || args[0] == "-l" || args[0] == "--list" {
				for _, a := range desk.Apps() {
					fmt.Fprintf(hc.Stdout, "  %-10s %s\n", a.Name, a.Help)
				}
				return 0
			}
			if _, err := desk.Launch(args[0], args[1:]...); err != nil {
				fmt.Fprintln(hc.Stderr, "open:", err)
				return 1
			}
			return 0
		})

	shell.RegisterApplet("term", "open another terminal window",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
			if _, err := desk.Launch("term", args...); err != nil {
				fmt.Fprintln(hc.Stderr, "term:", err)
				return 1
			}
			return 0
		})

	shell.RegisterApplet("apps", "list the windows this desk can open",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, _ []string) int {
			var b strings.Builder
			for _, a := range desk.Apps() {
				fmt.Fprintf(&b, "  %-10s %s\n", a.Name, a.Help)
			}
			fmt.Fprint(hc.Stdout, b.String())
			return 0
		})
}
