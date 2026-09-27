//go:build !linux

package sandbox

import (
	"fmt"
	"os"
)

// LandlockExecMain exists on every platform so cmd/rigfile can route the hidden subcommand without a build tag of
// its own. Real Landlock confinement is Linux-only (see sandbox_linux.go); wrap() on every other platform never
// produces an invocation of this, so reaching it here means something is wrong.
func LandlockExecMain(args []string) int {
	fmt.Fprintln(os.Stderr, "rigfile: internal: this build has no Landlock backend")
	return 2
}
