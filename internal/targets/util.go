package targets

import (
	"os"
	"os/exec"
)

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func lookPath(cmd string) bool { _, err := exec.LookPath(cmd); return err == nil }
