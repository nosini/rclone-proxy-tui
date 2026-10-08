package daemon

import "syscall"

// sysProcAttr makes the rclone children die with the daemon, even if it is
// killed with SIGKILL.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
