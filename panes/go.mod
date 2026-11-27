// The panes are adapters: each one knows both the desk and the program it
// puts in a window. That is why they are their own module rather than part
// of the desk — a window manager should not require a shell, and websh
// should not require a window manager, but something has to know both.
//
// The two commands live here too. Both compose rather than provide: cmd/desk
// is the demo, and cmd/desk-serve serves what it builds.
module github.com/0magnet/desk/panes

go 1.26.6

require (
	github.com/0magnet/afero v1.15.1-0.20261003211811-482680d00992
	github.com/0magnet/calvin v0.0.1-0.20261125222219-4228c77b6748
	github.com/0magnet/desk v0.0.3-0.20261126153730-5f6aa79c050e
	github.com/0magnet/seat v0.0.0-20261123110925-c4459967b4de
	github.com/0magnet/sh/v3 v3.13.2-0.20261024150911-145e01c63128
	github.com/0magnet/websh v0.0.1-0.20261127151940-35cc3448b035
	github.com/0magnet/xterm-go v0.0.1-0.20261123110635-ef9c274ca8f7
	github.com/creack/pty v1.1.24
	github.com/spf13/cobra v1.10.2
	golang.org/x/net v0.63.0
)

require (
	github.com/0magnet/bottle v0.0.1-0.20261127013808-02b522ecff04 // indirect
	github.com/0magnet/coloredcobra v1.0.4-0.20261012213101-faa6bd8f94c5 // indirect
	github.com/0magnet/u-root v0.16.1-0.20261003214924-44e47b732754 // indirect
	github.com/0magnet/winbox-go v0.0.1-0.20261126152900-d5f76c817f58 // indirect
	github.com/benhoyt/goawk v1.32.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/itchyny/gojq v0.12.20 // indirect
	github.com/itchyny/timefmt-go v0.1.9 // indirect
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/spf13/pflag v1.1.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/term v0.48.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)
