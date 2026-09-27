package source

import "github.com/rigfile/rigfile/internal/hashing"

// TreeHash is the content hash of a fetched rig directory: every file's path, size and SHA-256, and nothing else.
// The execute bit is left out because Windows has none, so the hash is the same on every OS (the lock would
// otherwise mismatch between the machine that pinned and the one that pulls). A root-level rigfile.lock is excluded.
func TreeHash(dir string) (string, error) {
	es, err := hashing.TreeEntries(dir)
	if err != nil {
		return "", err
	}
	out := es[:0]
	for _, e := range es {
		if e.Path == "rigfile.lock" {
			continue // derived output that `apply` writes next to the rig; the manifest and files are what is pinned
		}
		e.Exec = false
		out = append(out, e)
	}
	return hashing.TreeOf(out), nil
}
