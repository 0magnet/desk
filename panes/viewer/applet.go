//go:build js && wasm

package viewer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"

	"github.com/0magnet/desk"
)

// AppName is the desk app the view applet launches.
const AppName = "viewer"

// Register adds the viewer as a desk app and installs a `view` command in the
// shell, so a file produced by one window can be opened in another.
//
// There is no message passing behind this: both windows are in one binary over
// one filesystem, so writing the file *is* the hand-off and `view` only has to
// name it. That is the whole reason the two-window arrangement is cheap.
func Register(fs afero.Fs) {
	desk.Register(desk.App{
		Name:  AppName,
		Title: "viewer",
		Help:  "display an image or text file",
		Open: func(args []string) (desk.Pane, error) {
			if len(args) == 0 {
				return nil, fmt.Errorf("viewer: no file given")
			}
			return New(fs, args[0]), nil
		},
	})

	shell.RegisterApplet("view", "open a file in a viewer window",
		func(_ context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
			if len(args) == 0 {
				fmt.Fprintln(hc.Stderr, "usage: view FILE")
				return 2
			}
			path := args[0]
			if !strings.HasPrefix(path, "/") {
				path = filepath.Join(s.Dir(), path)
			}
			if _, err := s.FS.Stat(path); err != nil {
				fmt.Fprintln(hc.Stderr, "view:", err)
				return 1
			}
			if _, err := desk.LaunchOpts(AppName,
				desk.Options{Title: "viewer — " + filepath.Base(path)}, path); err != nil {
				fmt.Fprintln(hc.Stderr, "view:", err)
				return 1
			}
			return 0
		})
}
