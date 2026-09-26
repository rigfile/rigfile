//go:build !unix && !windows

package secrets

// lockFile is a no-op on platforms with neither flock nor LockFileEx: concurrent writers are last-writer-wins.
func lockFile(string) (func(), error) { return func() {}, nil }
