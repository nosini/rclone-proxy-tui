// Package service starts and stops the daemon, either through a systemd
// unit (so it survives logouts and reboots) or as a detached process.
package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nosini/rclone-proxy-tui/internal/state"
)

// UnitName is the systemd unit name.
const UnitName = "rclone-proxy-tui.service"

// Manager controls the daemon for one state directory.
type Manager struct {
	Paths state.Paths
	Exe   string
}

// New returns a Manager for the running executable.
func New(paths state.Paths) *Manager {
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
	}
	return &Manager{Paths: paths, Exe: exe}
}

func isRoot() bool { return os.Geteuid() == 0 }

// HasSystemd reports whether systemd is managing this machine.
func HasSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

// UnitPath is where the unit file is installed: a system unit for root, a
// user unit otherwise.
func UnitPath() string {
	if isRoot() {
		return filepath.Join("/etc/systemd/system", UnitName)
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", UnitName)
}

// Installed reports whether the systemd unit is installed.
func Installed() bool {
	_, err := os.Stat(UnitPath())
	return err == nil
}

func systemctl(args ...string) (string, error) {
	if !isRoot() {
		args = append([]string{"--user"}, args...)
	}
	var out bytes.Buffer
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), s)
	}
	return s, nil
}

// Unit renders the systemd unit file. The ExecStart colon prefix disables
// environment expansion so dollar signs in paths remain literal.
func (m *Manager) Unit() string {
	home, _ := os.UserHomeDir()
	target := "default.target"
	userLine := ""
	if isRoot() {
		target = "multi-user.target"
		if u, err := user.Current(); err == nil {
			userLine = "User=" + u.Username + "\n"
		}
	}
	return fmt.Sprintf(`[Unit]
Description=rclone-proxy-tui: serve rclone remotes to clients
Documentation=https://github.com/nosini/rclone-proxy-tui
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%sEnvironment=%s
ExecStart=:%s --dir %s daemon
Restart=on-failure
RestartSec=5
KillMode=mixed
TimeoutStopSec=90

[Install]
WantedBy=%s
`, userLine, systemdEscape("HOME="+home), systemdEscape(m.Exe), systemdEscape(m.Paths.Dir), target)
}

// Unit directives expand percent specifiers even inside quotes.
func systemdEscape(s string) string {
	s = strings.NewReplacer("%", "%%", `\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
	return `"` + s + `"`
}

// Install writes the unit, enables it and starts it. It returns notes to
// show the user.
func (m *Manager) Install() (string, error) {
	if !HasSystemd() {
		return "", fmt.Errorf("systemd is not available on this machine; use start/stop instead (or run `%s daemon` from your init system)", filepath.Base(m.Exe))
	}
	var notes []string
	if pid := m.Paths.DaemonPID(); pid != 0 && !Installed() {
		if err := m.stopPID(pid); err != nil {
			return "", err
		}
		notes = append(notes, "Stopped the manually started daemon.")
	}
	if err := state.WriteFileAtomic(UnitPath(), []byte(m.Unit()), 0o644); err != nil {
		return "", err
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return "", err
	}
	if _, err := systemctl("enable", "--now", UnitName); err != nil {
		return "", err
	}
	notes = append(notes, "Installed "+UnitPath()+" and started it. It starts on boot.")
	if !isRoot() {
		u, _ := user.Current()
		name := ""
		if u != nil {
			name = u.Username
		}
		if out, err := exec.Command("loginctl", "enable-linger", name).CombinedOutput(); err != nil {
			notes = append(notes, fmt.Sprintf("Could not enable lingering (%s). Without it the daemon stops when you log out of SSH. Run as root: loginctl enable-linger %s", strings.TrimSpace(string(out)), name))
		} else {
			notes = append(notes, "Enabled lingering so it keeps running after you log out.")
		}
	}
	return strings.Join(notes, "\n"), nil
}

// Uninstall stops and removes the unit.
func (m *Manager) Uninstall() error {
	if !Installed() {
		return nil
	}
	if _, err := systemctl("disable", "--now", UnitName); err != nil {
		return err
	}
	if err := os.Remove(UnitPath()); err != nil {
		return err
	}
	_, err := systemctl("daemon-reload")
	return err
}

// Running reports whether a daemon is running.
func (m *Manager) Running() bool {
	return m.Paths.DaemonPID() != 0
}

// Start starts the daemon.
func (m *Manager) Start() error {
	if Installed() && HasSystemd() {
		_, err := systemctl("start", UnitName)
		return err
	}
	if m.Running() {
		return nil
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(m.Exe, "--dir", m.Paths.Dir, "daemon")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// Reap it if it exits while the TUI is still open, so it doesn't linger
	// as a zombie that looks alive.
	go func() { _ = cmd.Wait() }()
	// Wait briefly for it to come up so errors are visible.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if m.Paths.DaemonPID() == pid {
			return nil
		}
		if !state.ProcessAlive(pid) {
			return fmt.Errorf("daemon exited immediately, see %s", m.Paths.Log())
		}
	}
	return fmt.Errorf("daemon did not start within 3 seconds, see %s", m.Paths.Log())
}

// Stop stops the daemon.
func (m *Manager) Stop() error {
	if Installed() && HasSystemd() {
		_, err := systemctl("stop", UnitName)
		return err
	}
	pid := m.Paths.DaemonPID()
	if pid == 0 {
		return nil
	}
	return m.stopPID(pid)
}

func (m *Manager) stopPID(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 900; i++ {
		// The daemon removes its pid file on exit.
		if !state.ProcessAlive(pid) || m.Paths.DaemonPID() != pid {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon (pid %d) did not stop", pid)
}

// Restart restarts the daemon.
func (m *Manager) Restart() error {
	if Installed() && HasSystemd() {
		_, err := systemctl("restart", UnitName)
		return err
	}
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
}

// Mode describes how the daemon is managed.
func (m *Manager) Mode() string {
	if Installed() {
		if isRoot() {
			return "systemd system service"
		}
		return "systemd user service"
	}
	return "manual (stops on reboot)"
}
