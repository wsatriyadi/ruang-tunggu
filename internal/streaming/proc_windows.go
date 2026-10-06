//go:build windows

package streaming

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup is a no-op on Windows, which has no POSIX process groups.
func setProcessGroup(cmd *exec.Cmd) {}

// signalInterrupt asks FFmpeg to exit cleanly. Windows maps both the console
// control events to the same handler ffmpeg installs for SIGINT/SIGTERM, so the
// drain-then-unpublish behaviour is preserved.
func signalInterrupt(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// CTRL_BREAK_EVENT reaches a process started without a console window.
	if err := cmd.Process.Signal(syscall.SIGINT); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// signalKill forces termination.
func signalKill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
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
	// OpenProcess succeeds only while the handle is still valid.
	const queryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(cmd.Process.Pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

// processID returns the child's pid, or 0 when unavailable.
func processID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	if pid, err := strconv.Atoi(strconv.Itoa(cmd.Process.Pid)); err == nil {
		return pid
	}
	return cmd.Process.Pid
}
