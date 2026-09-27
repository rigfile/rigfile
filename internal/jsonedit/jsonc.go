package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
)

// JSON with comments (VS Code's mcp.json, Zed's settings.json, many others) is edited by MASKING: comments and trailing commas
// are replaced by spaces, which yields a valid JSON document of exactly the same length, so every byte offset agrees with the
// original. The edit runs on that mask; the change it made (one contiguous splice) is then applied to the ORIGINAL bytes, so
// every comment outside the edited region survives. A comment inside a member that is replaced or removed goes with it.

// mask returns the masked copy and whether anything was masked. ok is false when the text cannot be masked safely (an
// unterminated string or block comment).
func mask(doc []byte) (shadow []byte, masked bool, ok bool) {
	out := append([]byte(nil), doc...)
	n := len(doc)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
		masked = true
	}
	// skip returns the index of the next significant byte at or after i, treating comments as space
	skip := func(i int) int {
		for i < n {
			switch {
			case doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\r' || doc[i] == '\n':
				i++
			case doc[i] == '/' && i+1 < n && doc[i+1] == '/':
				for i < n && doc[i] != '\n' {
					i++
				}
			case doc[i] == '/' && i+1 < n && doc[i+1] == '*':
				j := bytes.Index(doc[i+2:], []byte("*/"))
				if j < 0 {
					return n
				}
				i += j + 4
			default:
				return i
			}
		}
		return i
	}
	for i := 0; i < n; {
		c := doc[i]
		switch {
		case c == '"':
			i++
			for i < n && doc[i] != '"' {
				if doc[i] == '\\' {
					i++
				}
				i++
			}
			if i >= n {
				return nil, false, false
			}
			i++
		case c == '/' && i+1 < n && doc[i+1] == '/':
			j := i
			for j < n && doc[j] != '\n' {
				j++
			}
			blank(i, j)
			i = j
		case c == '/' && i+1 < n && doc[i+1] == '*':
			j := bytes.Index(doc[i+2:], []byte("*/"))
			if j < 0 {
				return nil, false, false
			}
			end := i + 2 + j + 2
			blank(i, end)
			i = end
		case c == ',':
			if k := skip(i + 1); k < n && (doc[k] == '}' || doc[k] == ']') {
				blank(i, i+1) // a trailing comma
			}
			i++
		default:
			i++
		}
	}
	return out, masked, true
}

// splice applies the change from shadow to edited onto the original document.
func splice(orig, shadow, edited []byte) []byte {
	p := 0
	for p < len(shadow) && p < len(edited) && shadow[p] == edited[p] {
		p++
	}
	s := 0
	for s < len(shadow)-p && s < len(edited)-p && shadow[len(shadow)-1-s] == edited[len(edited)-1-s] {
		s++
	}
	out := make([]byte, 0, len(orig)+len(edited))
	out = append(out, orig[:p]...)
	out = append(out, edited[p:len(edited)-s]...)
	out = append(out, orig[len(orig)-s:]...)
	return out
}

// wrap masks doc, runs f on the mask, and maps the result back. For a plain JSON document it is f(doc).
func wrap(doc []byte, f func(shadow []byte) ([]byte, error)) ([]byte, error) {
	shadow, masked, ok := mask(doc)
	if !ok {
		return nil, ErrInvalidJSON
	}
	if !masked {
		return f(doc)
	}
	edited, err := f(shadow)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(edited, shadow) {
		return doc, nil
	}
	return splice(doc, shadow, edited), nil
}

func shadowOf(doc []byte) []byte {
	if s, masked, ok := mask(doc); ok && masked {
		return s
	}
	return doc
}

// The exported API: plain JSON and JSON with comments and trailing commas.

func AppendStrings(doc []byte, path []string, vals []string) (out []byte, added []string, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, added, err = appendStrings(sh, path, vals)
		return o, err
	})
	return
}

func AppendRaw(doc []byte, path []string, raws []string) (out []byte, added []string, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, added, err = appendRaw(sh, path, raws)
		return o, err
	})
	return
}

func SetMissingString(doc []byte, path []string, value string) (out []byte, existing string, added bool, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, existing, added, err = setMissingString(sh, path, value)
		return o, err
	})
	return
}

func SetMissingRaw(doc []byte, path []string, raw string) (out []byte, existing string, added bool, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, existing, added, err = setMissingRaw(sh, path, raw)
		return o, err
	})
	return
}

func ReadValueRaw(doc []byte, path []string) (string, bool) { return readValueRaw(shadowOf(doc), path) }
func ReadString(doc []byte, path []string) (string, bool)   { return readString(shadowOf(doc), path) }
func ReadRaw(doc []byte, path []string) ([]string, error)   { return readRaw(shadowOf(doc), path) }
func ReadStrings(doc []byte, path []string) ([]string, error) {
	return readStrings(shadowOf(doc), path)
}

func RemoveRaw(doc []byte, path []string, raws []string) (out []byte, removed int, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, removed, err = removeRaw(sh, path, raws)
		return o, err
	})
	return
}

func RemoveStrings(doc []byte, path []string, vals []string) (out []byte, removed int, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, removed, err = removeStrings(sh, path, vals)
		return o, err
	})
	return
}

func ReplaceRaw(doc []byte, path []string, raw string) (out []byte, replaced bool, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, replaced, err = replaceRaw(sh, path, raw)
		return o, err
	})
	return
}

func RemoveMember(doc []byte, path []string) (out []byte, removed bool, err error) {
	out, err = wrap(doc, func(sh []byte) ([]byte, error) {
		var o []byte
		o, removed, err = removeMember(sh, path)
		return o, err
	})
	return
}

// Valid reports whether doc is JSON, or JSON with comments and trailing commas.
func Valid(doc []byte) bool {
	s, _, ok := mask(doc)
	return ok && json.Valid(s)
}

// Mask returns doc with comments and trailing commas replaced by spaces: valid JSON of the same length and byte
// offsets, safe to encoding/json.Unmarshal directly. For reading a JSONC file (capture, not editing it — an edit
// must go through the Set/Replace/Remove functions, which splice the change back onto the original bytes so
// comments outside it survive). An error means doc could not be masked safely (an unterminated string or block
// comment).
func Mask(doc []byte) ([]byte, error) {
	s, _, ok := mask(doc)
	if !ok {
		return nil, errUnterminated
	}
	return s, nil
}

var errUnterminated = errors.New("jsonedit: an unterminated string or block comment")
