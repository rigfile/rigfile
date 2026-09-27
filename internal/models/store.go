package models

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// FileName is the record of what Setup put on the machine, in the state directory.
const FileName = "models.json"

// Records is name -> what was installed.
type Records map[string]Installed

// Load reads the records; a missing file is empty.
func Load(stateDir string) (Records, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Records{}, nil
	}
	if err != nil {
		return nil, err
	}
	r := Records{}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return r, nil
}

// Save writes the records atomically.
func (r Records) Save(stateDir string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return platform.WritePrivate(filepath.Join(stateDir, FileName), append(b, '\n'))
}

// Names lists the recorded models in order.
func (r Records) Names() []string {
	out := make([]string, 0, len(r))
	for n := range r {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
