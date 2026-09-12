// Package docs is the demo's web shell — the page, its loader and the
// artwork — embedded from the directory it lives in.
//
// The embed is here rather than in the repository root for two reasons. The
// first is that a package should embed what sits beside it: these files are
// the docs, and the directive that carries them belongs with them rather than
// three lines into an unrelated file at the top of the module.
//
// The second is size, and it is the load-bearing one. `go mod vendor` copies
// every embed target of a package it vendors, so a root-level `//go:embed
// docs` put 16.4 MB of compiled wasm into the vendor tree of anything that
// imported this module for the desk chrome alone — and, for a consumer that
// commits its vendor directory, into its history on every bump. Splitting the
// embed by directory means an importer takes only what it names: this package
// is 0.1 MB, and the wasm lives in docs/go, which nothing but the demo server
// imports.
//
// Build tags cannot do this job. `go mod vendor` resolves embeds across every
// build configuration, so an embed behind an unset tag is copied anyway; only
// a package the consumer does not import is actually left behind.
//
// What is NOT here is desk.wasm. The page asks for it relatively, so a host
// serving this FS must answer /desk.wasm itself — which is the point for a
// host that already has a wasm of its own to serve, and is why the Pages site
// (which serves this directory straight from git, wasm included) is unaffected.
package docs

import (
	"embed"
	"io/fs"
)

//go:embed index.html wasm_exec.js CNAME desk-demo.png desk-goda-graph.svg
var shell embed.FS

// FS returns the demo shell: index.html, its wasm_exec.js loader, and the
// artwork. Paths are at the root of the returned FS, ready for
// http.FileServerFS.
//
// The wasm is absent by design — see the package comment.
func FS() fs.FS { return shell }
