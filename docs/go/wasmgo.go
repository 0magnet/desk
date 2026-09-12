// Package wasmgo is the standard-Go build of the demo, complete with its wasm.
//
// Separate from the parent docs package so that importing the shell does not
// drag 12.6 MB of compiled wasm into a consumer's vendor tree — see the
// package comment on docs. Only the demo server imports this.
//
// The package is named wasmgo rather than after its directory: the import path
// ends in /go, and "go" is a keyword and cannot name a package.
package wasmgo

import (
	"embed"
	"io/fs"
)

//go:embed index.html wasm_exec.js desk.wasm
var assets embed.FS

// FS returns the complete demo — page, loader and wasm — ready for
// http.FileServerFS. Unlike docs.FS it needs nothing from its host.
func FS() fs.FS { return assets }
