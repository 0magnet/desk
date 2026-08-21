//go:build js && wasm

// Command desk is the demo: a terminal in a window, and a viewer for whatever
// the terminal produces.
package main

import (
	"strings"
	"syscall/js"

	"github.com/0magnet/afero"

	"github.com/0magnet/desk"
	"github.com/0magnet/desk/panes/files"
	"github.com/0magnet/desk/panes/hostfs"
	"github.com/0magnet/desk/panes/hostterm"
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

	// THE WHOLE OF WHAT MAKES THIS A DESKTOP WITH SYSTEM ACCESS.
	//
	// The shell, the file manager and the viewer all work against an
	// afero.Fs, so substituting one implementation for another is enough to
	// make every one of them real at once. websh's interpreter still runs in
	// the tab; only the files it reads and writes stop being imaginary.
	//
	// Nothing below this line knows which filesystem it got.
	// /bin is kept synthetic. websh's PopulateBin writes a stub for every
	// applet so `ls /bin` shows the command set, which is right for a
	// filesystem in a tab and would otherwise mean the first thing this did
	// with a real home directory is scatter fifty empty files into it.
	if hf, ok := hostfs.New(); ok {
		term.SetFS(hostfs.Mount(hf, map[string]afero.Fs{
			"/bin": afero.NewMemMapFs(),
		}))
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
	// Registered whether or not an agent is there to talk to. Hiding it when
	// the page was served without --shell would mean the one way to find out
	// this exists is to already know: opening it and being told, in the
	// terminal, which flag turns it on is the discoverable version.
	desk.Register(desk.App{
		Name:   "host",
		Title:  "host shell",
		Help:   "a real shell on this machine (needs desk-serve --shell)",
		Width:  760,
		Height: 460,
		Open: func([]string) (desk.Pane, error) {
			return hostterm.New(), nil
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

	// Compositing is opt-in and reachable, rather than opt-in and unreachable.
	// It draws every window that is a canvas into one WebGL layer, and a
	// feature nothing can switch on is a feature nobody finds a bug in.
	if js.Global().Get("location").Get("search").String() != "" &&
		strings.Contains(js.Global().Get("location").Get("search").String(), "gl=1") {
		if err := desk.EnableCompositing(); err != nil {
			js.Global().Get("console").Call("warn", "desk: compositing unavailable: "+err.Error())
		}
	}

	desk.NewPanel()

	if _, err := desk.Launch("term"); err != nil {
		js.Global().Get("console").Call("error", err.Error())
	}
	select {}
}
