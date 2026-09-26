//go:build windows

package platform

import (
	"os"
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
