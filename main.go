// Command rclone-proxy-tui is a terminal UI for rclone that connects to
// upstream remotes and re-exposes them to clients over WebDAV, SFTP, S3,
// FTP and HTTP with generated per-client logins.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nosini/rclone-proxy-tui/internal/authproxy"
	"github.com/nosini/rclone-proxy-tui/internal/daemon"
	"github.com/nosini/rclone-proxy-tui/internal/service"
	"github.com/nosini/rclone-proxy-tui/internal/state"
	"github.com/nosini/rclone-proxy-tui/internal/tui"
)

var version = "dev"

const usage = `rclone-proxy-tui - manage rclone remotes and share them with clients

Usage:
  rclone-proxy-tui [--dir DIR]                 open the TUI (default)
  rclone-proxy-tui [--dir DIR] daemon          run the server daemon in the foreground
  rclone-proxy-tui [--dir DIR] status          print daemon status
  rclone-proxy-tui [--dir DIR] install-service install and start a systemd service
  rclone-proxy-tui [--dir DIR] uninstall-service
  rclone-proxy-tui version

Internal:
  rclone-proxy-tui auth-proxy PROTOCOL         called by rclone to check logins

State is kept in DIR (default: ~/.config/rclone-proxy-tui, or $` + state.EnvDir + `).
`

func main() {
	fs := flag.NewFlagSet("rclone-proxy-tui", flag.ExitOnError)
	dir := fs.String("dir", state.DefaultDir(), "state directory")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	_ = fs.Parse(os.Args[1:])
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	paths := state.Paths{Dir: absDir}
	args := fs.Args()
	cmd := "tui"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	err = nil
	switch cmd {
	case "tui":
		err = tui.Run(paths)
	case "daemon":
		err = daemon.Run(paths)
	case "auth-proxy":
		if len(args) != 1 {
			err = fmt.Errorf("usage: auth-proxy PROTOCOL")
			break
		}
		err = authproxy.Run(paths, state.Protocol(args[0]), os.Stdin, os.Stdout)
	case "status":
		err = printStatus(paths)
	case "install-service":
		var notes string
		notes, err = service.New(paths).Install()
		if notes != "" {
			fmt.Println(notes)
		}
	case "uninstall-service":
		err = service.New(paths).Uninstall()
	case "version", "--version":
		fmt.Println("rclone-proxy-tui", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func printStatus(paths state.Paths) error {
	st := paths.LoadStatus()
	if st == nil {
		fmt.Println("daemon: not running")
		return nil
	}
	fmt.Printf("daemon: running (pid %d, %s)\n", st.PID, service.New(paths).Mode())
	if st.RcloneVersion != "" {
		fmt.Println("rclone:", st.RcloneVersion)
	}
	for _, p := range state.AllProtocols {
		ps := st.Protocols[p]
		if ps == nil {
			continue
		}
		line := fmt.Sprintf("  %-7s %-9s %s", p.Title(), ps.State, ps.Addr)
		if ps.Note != "" {
			line += "  (" + ps.Note + ")"
		}
		if ps.Error != "" {
			line += "  " + strings.ReplaceAll(ps.Error, "\n", " | ")
		}
		fmt.Println(line)
	}
	return nil
}
