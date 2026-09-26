//go:build !windows

package platform

import "os"

// ForwardSignal passes a signal received by rigfile on to the child it launched.
func ForwardSignal(p *os.Process, sig os.Signal) error { return p.Signal(sig) }

// RenameReplace atomically moves oldpath over newpath (rename(2)).
func RenameReplace(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
