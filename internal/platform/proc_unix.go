//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"syscall"
)

// ForwardSignal passes a signal received by rigfile on to the child it launched.
func ForwardSignal(p *os.Process, sig os.Signal) error { return p.Signal(sig) }

// RenameReplace atomically moves oldpath over newpath (rename(2)).
func RenameReplace(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Detach makes cmd start in its own session, so closing the terminal that launched it does not stop it.
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// Terminate asks a process to stop (SIGTERM).
func Terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
