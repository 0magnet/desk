//go:build js && wasm

// Package files is a file manager over the shared filesystem.
//
// It is the other half of having a terminal: the shell is better at doing
// things to files, and a list is better at finding out what is there. Both work
// on the same filesystem, so a file written by a command appears here on the
// next refresh, and opening something here is the same `view` the shell has.
package files

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"syscall/js"
	"time"

	"github.com/0magnet/afero"

	"github.com/0magnet/desk"
	"github.com/0magnet/desk/dom"
)

const css = `
.dfm { display:flex; flex-direction:column; height:100%; background:#1b1f27;
       color:#d3d7cf; font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; }
.dfm-bar { flex:0 0 auto; display:flex; align-items:center; gap:6px;
           padding:6px 8px; background:#232833; border-bottom:1px solid #2e3540; }
.dfm-path { flex:1 1 auto; overflow:hidden; text-overflow:ellipsis;
            white-space:nowrap; color:#8fc6f0; }
.dfm-btn { cursor:pointer; padding:2px 8px; border-radius:4px; user-select:none;
           background:#2e3540; color:#d3d7cf; }
.dfm-btn:hover { background:#3b434f; }
.dfm-list { flex:1 1 auto; overflow:auto; min-height:0; }
.dfm-row { display:flex; gap:8px; padding:3px 10px; cursor:default; }
.dfm-row:hover { background:#252b36; }
.dfm-row.dir .dfm-name { color:#8fc6f0; }
.dfm-name { flex:1 1 auto; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.dfm-size { flex:0 0 auto; color:#7d8694; }
.dfm-time { flex:0 0 auto; color:#5b6472; width:11ch; text-align:right; }
.dfm-empty { padding:14px; color:#7d8694; }
`

// Pane lists a directory.
type Pane struct {
	fs  afero.Fs
	dir string

	root, list, pathEl js.Value
	fns                dom.Funcs
}

// New opens at dir, or /home/user if empty.
func New(fs afero.Fs, dir string) *Pane {
	if dir == "" {
		dir = "/home/user"
	}
	return &Pane{fs: fs, dir: dir}
}

// Mount builds the listing.
func (p *Pane) Mount(el js.Value) error {
	dom.Stylesheet("desk-files-css", css)

	p.pathEl = dom.El("div", dom.Class("dfm-path"))
	p.list = dom.El("div", dom.Class("dfm-list"))

	up := dom.El("div", dom.Class("dfm-btn"), dom.Text("up"),
		p.fns.On("click", func(js.Value) { p.chdir(path.Dir(p.dir)) }))
	home := dom.El("div", dom.Class("dfm-btn"), dom.Text("home"),
		p.fns.On("click", func(js.Value) { p.chdir("/home/user") }))
	refresh := dom.El("div", dom.Class("dfm-btn"), dom.Text("refresh"),
		p.fns.On("click", func(js.Value) { p.render() }))

	p.root = dom.El("div", dom.Class("dfm"),
		dom.Child(dom.El("div", dom.Class("dfm-bar"),
			dom.Child(up), dom.Child(home), dom.Child(p.pathEl), dom.Child(refresh))),
		dom.Child(p.list))

	el.Call("appendChild", p.root)
	p.render()
	return nil
}

func (p *Pane) chdir(dir string) {
	if dir == "" {
		dir = "/"
	}
	p.dir = path.Clean(dir)
	p.render()
}

func (p *Pane) render() {
	p.pathEl.Set("textContent", p.dir)
	dom.Clear(p.list)

	infos, err := afero.ReadDir(p.fs, p.dir)
	if err != nil {
		p.list.Call("appendChild",
			dom.El("div", dom.Class("dfm-empty"), dom.Text(err.Error())))
		return
	}
	// Directories first, then names — the order a person expects rather than
	// the order the filesystem happens to return.
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].IsDir() != infos[j].IsDir() {
			return infos[i].IsDir()
		}
		return infos[i].Name() < infos[j].Name()
	})
	if len(infos) == 0 {
		p.list.Call("appendChild",
			dom.El("div", dom.Class("dfm-empty"), dom.Text("empty")))
		return
	}

	for _, info := range infos {
		name, isDir, full := info.Name(), info.IsDir(), path.Join(p.dir, info.Name())
		class := "dfm-row"
		label := name
		size := humanSize(info.Size())
		if isDir {
			class += " dir"
			label = name + "/"
			size = ""
		}
		row := dom.El("div", dom.Class(class),
			dom.Child(dom.El("span", dom.Class("dfm-name"), dom.Text(label))),
			dom.Child(dom.El("span", dom.Class("dfm-size"), dom.Text(size))),
			dom.Child(dom.El("span", dom.Class("dfm-time"),
				dom.Text(info.ModTime().Format(time.RFC3339[:10])))),
			p.fns.On("dblclick", func(js.Value) { p.open(full, isDir) }))
		p.list.Call("appendChild", row)
	}
}

// open descends into a directory, or hands a file to the viewer. It goes
// through the desk rather than knowing what a viewer is, so a project that
// registers a better viewer gets it here for free.
func (p *Pane) open(full string, isDir bool) {
	if isDir {
		p.chdir(full)
		return
	}
	if _, ok := desk.Lookup("viewer"); !ok {
		return
	}
	if _, err := desk.LaunchOpts("viewer",
		desk.Options{Title: "viewer — " + path.Base(full)}, full); err != nil {
		js.Global().Get("console").Call("warn", "files: "+err.Error())
	}
}

// Close releases the listeners.
func (p *Pane) Close() { p.fns.Release() }

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp]), ".0")
}
