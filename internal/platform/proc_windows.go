//go:build windows

package platform

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ForwardSignal ends the child: Windows cannot deliver Ctrl-C or SIGTERM to another process through
// os.Process.Signal (only Kill is implemented), so an interrupt of `rigfile exec` terminates the server it wraps.
func ForwardSignal(p *os.Process, sig os.Signal) error { return p.Kill() }

// RenameReplace moves oldpath over newpath. Windows refuses the move while another process (an editor, a
// virus scanner, the tool being configured) has the destination open, usually for milliseconds, so it retries
// briefly before giving up.
func RenameReplace(oldpath, newpath string) error {
	var err error
	for i := 0; i < 8; i++ {
		if err = os.Rename(oldpath, newpath); err == nil {
			return nil
		}
		time.Sleep(time.Duration(25<<i) * time.Millisecond / 2)
	}
	return err
}

// Detach makes cmd start without a console and outside this process's group, so closing the terminal that launched it
// does not stop it (DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP).
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200, HideWindow: true}
}

// Terminate ends a process (Windows has no gentler cross-process signal).
func Terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
