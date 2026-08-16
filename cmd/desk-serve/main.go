// Command desk-serve serves the desk web view from a single binary.
//
// The page, the wasm and its loader are embedded, so this needs no checkout, no
// network and nothing else installed. It exists because a wasm page cannot be
// opened with file:// — the browser refuses to instantiate a module fetched
// that way — so trying it locally otherwise means finding a static server and
// getting its MIME types right.
package main

import (
	"flag"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/0magnet/desk"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	open := flag.Bool("open", true, "open a browser at the served page")
	flag.Parse()

	// Some systems have a registry entry mapping .wasm to something else, and
	// a wrong Content-Type makes instantiateStreaming refuse the module with
	// an error that says nothing about MIME types.
	if err := mime.AddExtensionType(".wasm", "application/wasm"); err != nil {
		log.Printf("desk: could not register the wasm MIME type: %v", err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("desk: %v", err)
	}
	url := "http://" + ln.Addr().String() + "/"
	if strings.HasPrefix(ln.Addr().String(), "[::]") || strings.HasPrefix(*addr, ":") {
		url = fmt.Sprintf("http://localhost:%d/", ln.Addr().(*net.TCPAddr).Port)
	}

	mux := http.NewServeMux()
	mux.Handle("/", noCache(http.FileServerFS(desk.Assets())))

	fmt.Printf("desk: serving on %s\n", url)
	if *open {
		go browse(url)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

// noCache keeps a rebuilt wasm from being served out of the browser cache,
// which otherwise makes a fresh build look like it changed nothing.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func browse(url string) {
	time.Sleep(300 * time.Millisecond) // let the listener settle first
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	if err := exec.Command(cmd, append(args, url)...).Start(); err != nil {
		log.Printf("desk: could not open a browser (%v); visit %s", err, url)
	}
}
