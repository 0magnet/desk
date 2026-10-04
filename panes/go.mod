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
	github.com/0magnet/calvin v0.0.0
	github.com/0magnet/coloredcobra v1.0.4-0.20260908180055-b9d8b508df51
	github.com/0magnet/desk v0.0.1-0.20261004014112-6745b410dec7
	github.com/0magnet/sh/v3 v3.13.2-0.20261004194540-aa2d6e4a31a5
	github.com/0magnet/websh v0.0.1-0.20261004200853-adb76a8fa4c2
	github.com/0magnet/xterm-go v0.0.1-0.20261004020305-36b45f096b30
	github.com/creack/pty v1.1.24
	github.com/spf13/cobra v1.10.2
	golang.org/x/net v0.59.0
)

require (
	github.com/0magnet/u-root v0.16.1-0.20261003214924-44e47b732754 // indirect
	github.com/0magnet/winbox-go v0.0.0 // indirect
	github.com/benhoyt/goawk v1.32.0 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/itchyny/gojq v0.12.19 // indirect
	github.com/itchyny/timefmt-go v0.1.9 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
