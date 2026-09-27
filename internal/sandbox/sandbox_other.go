//go:build !darwin && !linux

package sandbox

import "fmt"

// wrap: no confinement backend exists for this platform (Windows is Stage 3 for everything else in this project;
// Level 2 itself is macOS+Linux only). Fail closed rather than launch unconfined.
func wrap(prog string, args []string, policy Policy) (*Wrapped, error) {
	return nil, fmt.Errorf("%w", ErrUnsupported)
}
