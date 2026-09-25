//go:build !unix

package secrets

// lockFile is a no-op until Windows support (Stage 3, LockFileEx): concurrent writers are last-writer-wins.
func lockFile(string) (func(), error) { return func() {}, nil }
