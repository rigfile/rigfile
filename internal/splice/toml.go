package splice

import (
	"errors"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

var (
	// ErrInvalidBase means the file was not valid TOML before we touched it; we refuse to edit it.
	ErrInvalidBase = errors.New("splice: existing TOML is invalid; refusing to edit")
	// ErrInvalidResult means inserting the region would produce invalid TOML, typically because the
	// user already defines the same table or key (a conflict for the plan screen, not a crash).
	ErrInvalidResult = errors.New("splice: result would not be valid TOML (conflict with existing content?)")
)

func checkTOML(b []byte) error {
	var v map[string]any
	return toml.Unmarshal(b, &v)
}

// UpsertTOML is Upsert with Hash-style markers plus a validity check. The TOML parser is used only
// to validate, never to re-serialise, so comments and layout outside the region survive untouched.
func UpsertTOML(doc []byte, id string, body []byte, opt Options) ([]byte, bool, error) {
	if err := checkTOML(doc); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalidBase, err)
	}
	out, changed, err := Upsert(doc, Hash, id, body, opt)
	if err != nil {
		return nil, false, err
	}
	if changed {
		if err := checkTOML(out); err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrInvalidResult, err)
		}
	}
	return out, changed, nil
}
