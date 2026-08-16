//go:build js && wasm

// Command desk is the demo: a terminal in a window, and a viewer for whatever
// the terminal produces.
package main

import (
	"syscall/js"

	"github.com/0magnet/desk"
	"github.com/0magnet/desk/panes/files"
	"github.com/0magnet/desk/panes/term"
	"github.com/0magnet/desk/panes/viewer"
)

const greeting = "" +
	"\x1b[1;35mdesk\x1b[0m — windows over panes, in WebAssembly\r\n" +
	"\x1b[2mthe shell is \x1b[0m\x1b[1mwebsh\x1b[0m\x1b[2m, the windows are \x1b[0m\x1b[1mwinbox-go\x1b[0m\r\n\r\n" +
	"try: \x1b[1mecho hello > note.txt && view note.txt\x1b[0m\r\n" +
	"     \x1b[1mopen files\x1b[0m  ·  \x1b[1mterm\x1b[0m  ·  or the menu, bottom left\r\n\r\n"

func main() {
	if el := js.Global().Get("document").Call("getElementById", "desktop"); el.Truthy() {
		desk.SetRoot(el)
	}

	desk.Register(desk.App{
		Name:      "term",
		Maximized: true,
		Title:     "terminal",
		Help:      "a shell",
		Width:     760,
		Height:    460,
		Open: func([]string) (desk.Pane, error) {
			return term.New(greeting, "desk"), nil
		},
	})
	desk.Register(desk.App{
		Name:   "files",
		Title:  "files",
		Help:   "browse the filesystem",
		Width:  560,
		Height: 420,
		Open: func(args []string) (desk.Pane, error) {
			dir := ""
			if len(args) > 0 {
				dir = args[0]
			}
			return files.New(term.FS(), dir), nil
		},
	})
	viewer.Register(term.FS())
	registerLauncherApplets()

	desk.NewPanel()

	if _, err := desk.Launch("term"); err != nil {
		js.Global().Get("console").Call("error", err.Error())
	}
	select {}
}
