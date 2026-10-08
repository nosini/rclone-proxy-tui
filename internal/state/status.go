package state

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Process states reported by the daemon.
const (
	ProcDisabled = "disabled"
	ProcStarting = "starting"
	ProcRunning  = "running"
	ProcError    = "error"
	ProcStopped  = "stopped"
)

// ProtoStatus is the runtime state of one rclone serve process.
type ProtoStatus struct {
	State    string    `json:"state"`
	Addr     string    `json:"addr,omitempty"`
	Error    string    `json:"error,omitempty"`
	Note     string    `json:"note,omitempty"`
	Since    time.Time `json:"since"`
	Restarts int       `json:"restarts,omitempty"`
	PID      int       `json:"pid,omitempty"`
}

// Status is written by the daemon so the TUI can show what is going on.
type Status struct {
	PID           int                       `json:"pid"`
	Started       time.Time                 `json:"started"`
	Updated       time.Time                 `json:"updated"`
	RcloneVersion string                    `json:"rclone_version,omitempty"`
	Message       string                    `json:"message,omitempty"`
	Protocols     map[Protocol]*ProtoStatus `json:"protocols"`
}

// StatusStale is how old a status file can be before the daemon is
// considered dead.
const StatusStale = 15 * time.Second

// LoadStatus reads the daemon status. It returns nil if there is no live
// daemon.
func (p Paths) LoadStatus() *Status {
	b, err := os.ReadFile(p.Status())
	if err != nil {
		return nil
	}
	st := &Status{}
	if json.Unmarshal(b, st) != nil {
		return nil
	}
	if !ProcessAlive(st.PID) || time.Since(st.Updated) > StatusStale {
		return nil
	}
	return st
}

// SaveStatus writes the daemon status.
func (p Paths) SaveStatus(st *Status) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(p.Status(), b, 0o600)
}

// DaemonPID returns the PID from the pid file if that process is alive.
func (p Paths) DaemonPID() int {
	b, err := os.ReadFile(p.PID())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || !ProcessAlive(pid) {
		return 0
	}
	return pid
}

// ProcessAlive reports whether a process with the pid exists.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
