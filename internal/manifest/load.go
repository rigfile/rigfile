package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.yaml.in/yaml/v3"
)

// FileName is the manifest file inside a rig directory.
const FileName = "rigfile.yaml"

// Loaded is a manifest read from a rig directory.
type Loaded struct {
	M    *Manifest
	Dir  string // absolute path of the rig directory (all rig-relative paths resolve against it)
	Raw  []byte
	Hash string // sha256 of Raw, hex
}

var (
	validatorOnce sync.Once
	validator     *Validator
	validatorErr  error
)

// sharedValidator compiles the schema once per process.
func sharedValidator() (*Validator, error) {
	validatorOnce.Do(func() { validator, validatorErr = NewValidator() })
	return validator, validatorErr
}

// Parse validates and decodes manifest bytes.
func Parse(data []byte) (*Manifest, error) {
	v, err := sharedValidator()
	if err != nil {
		return nil, err
	}
	if err := v.ValidateYAML(data); err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &m, nil
}

// Load reads <dir>/rigfile.yaml, validates it against the schema and decodes it. It does NOT check
// that referenced files exist; call Check for that (it needs the directory).
func Load(dir string) (*Loaded, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(abs, FileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s has no %s", abs, FileName)
		}
		return nil, err
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(abs, FileName), err)
	}
	sum := sha256.Sum256(raw)
	return &Loaded{M: m, Dir: abs, Raw: raw, Hash: hex.EncodeToString(sum[:])}, nil
}
