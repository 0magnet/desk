// The panes are adapters: each one knows both the desk and the program it
// puts in a window. That is why they are their own module rather than part
// of the desk — a window manager should not require a shell, and websh
// should not require a window manager, but something has to know both.
//
// The two commands live here too. Both compose rather than provide: cmd/desk
// is the demo, and cmd/desk-serve serves what it builds.
module github.com/0magnet/desk/panes

go 1.26

require (
	github.com/0magnet/afero v1.15.1-0.20260816202415-9f9d46a34dcd
	github.com/0magnet/calvin v0.0.0-20260818215653-c62af7624521
	github.com/0magnet/desk v0.0.0-20260819004606-d85d4c31e78a
	github.com/0magnet/sh/v3 v3.13.2-0.20260818190530-13d0024da85c
	github.com/0magnet/websh v0.0.0-20260818190700-3c413dff1867
	github.com/ivanpirog/coloredcobra v1.0.1
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/0magnet/u-root v0.16.1-0.20260814161052-156e0b67262b // indirect
	github.com/0magnet/winbox-go v0.0.0-20260819232003-5105b3373be4 // indirect
	github.com/0magnet/xterm-go v0.0.0-20260817124232-e65805e044b1 // indirect
	github.com/benhoyt/goawk v1.31.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/itchyny/gojq v0.12.19 // indirect
	github.com/itchyny/timefmt-go v0.1.8 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/0magnet/desk => ../
