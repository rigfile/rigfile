// Package manifest parses and validates rigfile.yaml against the embedded JSON Schema.
//
// Scope of this package in the Stage 1 spike: structural validation only. The rules JSON Schema
// cannot express (real secret scanning, pinning of free-form args, cross-references, layer
// merging) are listed in docs/merge-semantics.md Appendix A and are not implemented yet.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/digitaldreamer3462/rigfile/schema"
)

const schemaURL = "https://rigfile.dev/schema/rigfile.v1.json"

// Validator validates manifests against schema/rigfile.v1.json. Safe for concurrent use.
type Validator struct {
	sch *jsonschema.Schema
}

// NewValidator compiles the embedded schema.
func NewValidator() (*Validator, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.ManifestV1))
	if err != nil {
		return nil, fmt.Errorf("parse embedded schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	sch, err := c.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return &Validator{sch: sch}, nil
}

// ValidationError is returned when the document is well-formed but violates the schema.
// Problems are already flattened to one line each: "<json pointer>: <message>".
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "manifest is invalid:\n  " + strings.Join(e.Problems, "\n  ")
}

// ValidateYAML parses a rigfile.yaml document and validates it. A nil error means valid.
func (v *Validator) ValidateYAML(data []byte) error {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	return v.ValidateValue(doc)
}

// ValidateValue validates an already-decoded document (maps/slices/scalars).
func (v *Validator) ValidateValue(doc any) error {
	// Round-trip through JSON so numbers/maps have exactly the shapes the schema library expects.
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("convert to json: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	if err := v.sch.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return &ValidationError{Problems: flatten(ve)}
		}
		return err
	}
	return nil
}

// flatten returns the leaf causes of a validation error, one line each.
func flatten(ve *jsonschema.ValidationError) []string {
	// A printer per call: message.Printer is not documented as safe for concurrent use, and
	// LocalizedString panics on a nil printer.
	p := message.NewPrinter(language.English)
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			out = append(out, fmt.Sprintf("%s: %s", loc, e.ErrorKind.LocalizedString(p)))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(out) == 0 {
		out = []string{ve.Error()}
	}
	return out
}
