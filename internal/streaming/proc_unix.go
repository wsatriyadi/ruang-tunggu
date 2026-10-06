//go:build unix

package streaming

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so a stop signal
// reaches FFmpeg and anything it spawns, leaving no orphan holding the
// RTMPS socket open.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalInterrupt asks the process group to shut down cleanly. FFmpeg maps
// SIGINT and SIGTERM to the same handler, which drains the muxer and sends
// FCUnpublish so YouTube records a proper end of broadcast.
func signalInterrupt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGINT)
}

// signalKill forces termination of the process group. The connection drops
// without an unpublish, so it is only used after the grace period expires.
func signalKill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// processAlive reports whether the process still exists.
func processAlive(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	if cmd.ProcessState != nil {
		return false
	}
	return syscall.Kill(cmd.Process.Pid, 0) == nil
}

// processID returns the child's pid, or 0 when unavailable.
func processID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}
