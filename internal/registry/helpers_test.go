package registry_test

import (
	"os"
	"path/filepath"
	"testing"
)

func filepathGlob(t *testing.T) ([]string, error) {
	files, err := filepath.Glob("*.go")
	var out []string
	for _, f := range files {
		if len(f) > 8 && f[len(f)-8:] == "_test.go" {
			continue
		}
		out = append(out, f)
	}
	return out, err
}

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
