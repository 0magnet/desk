# desk

Windows over panes, in WebAssembly. A small desktop shell for the browser:
[winbox-go](https://github.com/0magnet/winbox-go) supplies the windows,
[websh](https://github.com/0magnet/websh) supplies a shell, and a pane is
anything that renders into a DOM element.

**[Live demo](https://0magnet.github.io/desk/)** · TinyGo build ·
[standard Go build](https://0magnet.github.io/desk/go/)

```
desk:~$ echo hello > note.txt && view note.txt    a command opens a window
desk:~$ open files                                or use the menu, bottom left
```

## What it is

```
desk.go        the registry: apps, windows, mounting, teardown
panel.go       the panel — Applications menu, task buttons, clock
dom/           element building without the syscall/js ceremony
panes/term     a websh shell
panes/files    a file manager
panes/viewer   an image and text viewer, plus the `view` command
cmd/desk       the demo
cmd/desk-serve a native binary that serves it, assets embedded
```

The shell knows nothing about terminals, images or files. A pane is:

```go
type Pane interface {
	Mount(el js.Value) error
	Close()
}
```

An app that a page opens with should set `Maximized`, so the desktop starts
with something filling it rather than a small window adrift in empty space.

`Resizer` is optional and most panes do not want it — one laid out with CSS is
resized by the browser, and one that watches its own element hears about it from
a `ResizeObserver`. The terminal pane does not implement it.

That is the point of the arrangement: a project brings its own pane rather than
waiting for this package to grow support for it.

## Running it

```
go run ./cmd/desk-serve          # serves the built demo, opens a browser
./build.sh                       # rebuild both          -> docs/ and docs/go/
./build.sh tinygo                # TinyGo only
./build.sh go                    # standard Go only
```

Both toolchains are carried. TinyGo is the default because the binary is a
quarter the size and is fetched before anything appears; the standard Go build
is a click away in the page header, because TinyGo occasionally miscompiles
something and having the other one to hand is how you find out that is what
happened.

`desk-serve` embeds `docs/`, so it needs no checkout and no network. It exists
because a wasm page cannot be opened over `file://` — the browser refuses to
instantiate a module fetched that way — and because a wrong `Content-Type` on
the `.wasm` makes `instantiateStreaming` fail with an error that mentions
nothing about MIME types.

## The filesystem is the channel

Every pane shares one [afero](https://github.com/0magnet/afero) filesystem, so
handing a file from one window to another needs no message passing:

```
desk:~$ view report.svg
```

`view` only has to name the file. Writing it *was* the hand-off. The file
manager opens files the same way, through `desk.Lookup("viewer")` rather than
knowing what a viewer is — so registering a better viewer replaces it
everywhere at once.

## Fitting

A terminal in a window is resized by its container, never by the browser
window, so a `window.resize` listener never hears about it. xterm-go's
`AutoFit` observes the parent element instead:

```
window body  760x425  ->  terminal canvas  738x420
window body 1000x565  ->  terminal canvas  981x560
window body  500x285  ->  terminal canvas  477x280
```

## What it does not do

No workspaces, no session management, no multi-monitor, no file operations
beyond browsing. The panel and the menu are what make a collection of windows
read as a desktop; the rest is refinement on top of those two.
