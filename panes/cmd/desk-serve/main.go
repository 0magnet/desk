//go:build !js

// Command desk-serve serves the desk web view from a single binary.
//
// The page, the wasm and its loader are embedded, so this needs no checkout, no
// network and nothing else installed. It exists because a wasm page cannot be
// opened with file:// — the browser refuses to instantiate a module fetched
// that way — so trying it locally otherwise means finding a static server and
// getting its MIME types right.
package main

import (
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/0magnet/calvin"
	"github.com/0magnet/desk"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
)

var (
	addr     string
	open     bool
	shell    bool
	shcmd    string
	hostFS   bool
	fsRoot   string
	hostAuth bool
)

func init() {
	RootCmd.Flags().StringVarP(&addr, "addr", "a", "127.0.0.1:8080", "address to listen on")
	RootCmd.Flags().BoolVarP(&open, "open", "o", true, "open a browser at the served page")
	RootCmd.Flags().BoolVarP(&shell, "shell", "s", false, "let the page run a real shell on this machine")
	RootCmd.Flags().StringVar(&shcmd, "shell-cmd", "", "what --shell starts (default $SHELL, then /bin/sh)")
	RootCmd.Flags().BoolVarP(&hostFS, "fs", "f", false, "let the page read and write this machine's files")
	RootCmd.Flags().StringVar(&fsRoot, "fs-root", "", "confine --fs to this subtree (default: the whole filesystem)")
	RootCmd.Flags().BoolVar(&hostAuth, "auth", false, "print the token instead of putting it in the page, and ask for it (for shared machines)")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd is the root command
var RootCmd = &cobra.Command{
	Use:                   "desk-serve",
	Short:                 "serve the desk web view",
	Long:                  calvin.AsciiFont("desk-serve") + "\nserve the desk web view",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		// Some systems have a registry entry mapping .wasm to something else, and
		// a wrong Content-Type makes instantiateStreaming refuse the module with
		// an error that says nothing about MIME types.
		if err := mime.AddExtensionType(".wasm", "application/wasm"); err != nil {
			log.Printf("desk: could not register the wasm MIME type: %v", err)
		}

		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("desk: %v", err)
		}
		url := "http://" + ln.Addr().String() + "/"
		if strings.HasPrefix(ln.Addr().String(), "[::]") || strings.HasPrefix(addr, ":") {
			url = fmt.Sprintf("http://localhost:%d/", ln.Addr().(*net.TCPAddr).Port)
		}

		mux := http.NewServeMux()
		page := noCache(http.FileServerFS(desk.Assets()))

		opt := hostOptions{wantShell: shell, wantFS: hostFS, shell: shcmd, fsRoot: fsRoot, auth: hostAuth}
		if opt.wantShell || opt.wantFS {
			// Refusing rather than warning. The Origin check makes a
			// non-loopback listener less bad than it sounds, but "a shell,
			// reachable from the network, because a flag defaulted" is not a
			// sentence this should ever be able to produce. Bind loopback.
			if a, ok := ln.Addr().(*net.TCPAddr); !ok || !a.IP.IsLoopback() {
				log.Fatalf("desk: host access needs a loopback address; %s is reachable from elsewhere", ln.Addr())
			}
			cfg, err := mountHostAgent(mux, ln, opt)
			if err != nil {
				log.Fatalf("desk: %v", err)
			}
			if page, err = injectHostConfig(desk.Assets(), page, cfg); err != nil {
				log.Fatalf("desk: %v", err)
			}
			warnAboutHostAccess(opt)
		}
		mux.Handle("/", page)

		fmt.Printf("desk: serving on %s\n", url)
		if open {
			go browse(url)
		}
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		log.Fatal(srv.Serve(ln))
	},
}

func main() {
	cc.Init(&cc.Config{
		RootCmd:         RootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
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
	// The command is chosen by the switch above and the URL is this process's
	// own listener, so nothing here comes from outside.
	if err := exec.Command(cmd, append(args, url)...).Start(); err != nil { //nolint:gosec
		log.Printf("desk: could not open a browser (%v); visit %s", err, url)
	}
}

const help = "{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}" +
	"Available Commands:{{range .Commands}}  {{if and (ne .Name \"completion\") .IsAvailableCommand}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
