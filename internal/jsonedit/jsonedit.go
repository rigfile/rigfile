// Package jsonedit makes targeted additions to JSON documents (Claude Code's settings.json) without
// re-serialising them: every byte outside the inserted text is preserved, so key order, indentation,
// spacing and line endings the user chose survive (ADR 0001 §8 item 3).
//
// It only ever ADDS values. There is deliberately no delete or replace: the permissions merge rules
// (docs/merge-semantics.md §4.5) say a layer can never remove a deny.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// ErrNotObject / ErrWrongType report a document whose shape prevents a safe edit.
var (
	ErrInvalidJSON = errors.New("jsonedit: document is not valid JSON")
	ErrWrongType   = errors.New("jsonedit: value at path has an unexpected type")
)

// AppendStrings ensures every value in vals is present in the string array at path, creating the
// array (and any missing parent objects) if necessary. It returns the new document and the values
// that were actually added (values already present, and duplicates within vals, are skipped).
// An empty or whitespace-only doc is treated as {}.
func AppendStrings(doc []byte, path []string, vals []string) (out []byte, added []string, err error) {
	if len(path) == 0 {
		return nil, nil, fmt.Errorf("jsonedit: empty path")
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		doc = []byte("{}\n")
	}
	if !gjson.ValidBytes(doc) {
		return nil, nil, ErrInvalidJSON
	}
	if root := bytes.TrimSpace(doc); root[0] != '{' {
		return nil, nil, fmt.Errorf("%w: root is not an object", ErrWrongType)
	}
	st := detectStyle(doc)

	// Walk down as far as the document already goes.
	prefix := []string{}
	for k := 0; k < len(path)-1; k++ {
		child := lookup(doc, append(prefix, path[k]))
		if !child.Exists() {
			// Missing from here on down: build the whole remaining branch and insert it as one member.
			todo := dedupe(vals)
			if len(todo) == 0 {
				return doc, nil, nil
			}
			raw := buildBranch(st, len(prefix)+1, path[k+1:], todo)
			out, err := insertMember(doc, st, prefix, path[k], raw)
			return out, todo, err
		}
		if !child.IsObject() {
			return nil, nil, fmt.Errorf("%w: %s is not an object", ErrWrongType, strings.Join(append(prefix, path[k]), "."))
		}
		prefix = append(prefix, path[k])
	}

	last := path[len(path)-1]
	arr := lookup(doc, append(prefix, last))
	if !arr.Exists() {
		todo := dedupe(vals)
		if len(todo) == 0 {
			return doc, nil, nil
		}
		raw := buildArray(st, len(prefix)+1, todo)
		out, err := insertMember(doc, st, prefix, last, raw)
		return out, todo, err
	}
	if !arr.IsArray() {
		return nil, nil, fmt.Errorf("%w: %s is not an array", ErrWrongType, strings.Join(append(prefix, last), "."))
	}
	have := map[string]bool{}
	for _, e := range arr.Array() {
		if e.Type != gjson.String {
			return nil, nil, fmt.Errorf("%w: %s contains a non-string element", ErrWrongType, strings.Join(append(prefix, last), "."))
		}
		have[e.String()] = true
	}
	for _, v := range dedupe(vals) {
		if !have[v] {
			added = append(added, v)
		}
	}
	if len(added) == 0 {
		return doc, nil, nil
	}
	out, err = appendToArray(doc, st, len(prefix)+1, arr, added)
	return out, added, err
}

// --- style detection ------------------------------------------------------------------------

type style struct {
	eol     string // "\n" or "\r\n"
	unit    string // one indentation level
	spaced  bool   // single-line arrays use ", " (vs ",")
	compact bool   // the whole document is on one line: insert compactly too
	colon   string // ":" or ": " between key and value
}

func detectStyle(doc []byte) style {
	st := style{eol: "\n", unit: "  ", colon: ": "}
	if i := bytes.IndexByte(doc, '\n'); i > 0 && doc[i-1] == '\r' {
		st.eol = "\r\n"
	}
	// First indented line decides the unit.
	for _, line := range strings.Split(string(doc), "\n") {
		trim := strings.TrimLeft(line, " \t")
		if trim != "" && len(trim) != len(line) {
			st.unit = line[:len(line)-len(trim)]
			break
		}
	}
	st.spaced = bytes.Contains(doc, []byte(`", "`))
	st.compact = !bytes.Contains(bytes.TrimSpace(doc), []byte("\n"))
	if !bytes.Contains(doc, []byte(`": `)) {
		st.colon = ":"
	}
	return st
}

func (s style) indent(level int) string { return strings.Repeat(s.unit, level) }

// --- lookup ---------------------------------------------------------------------------------

// gjsonEscape escapes a key so gjson treats it literally.
func gjsonEscape(k string) string {
	var b strings.Builder
	for _, r := range k {
		switch r {
		case '.', '*', '?', '|', '#', '@', '\\', '!', '=', '<', '>', '%':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func lookup(doc []byte, segs []string) gjson.Result {
	esc := make([]string, len(segs))
	for i, s := range segs {
		esc[i] = gjsonEscape(s)
	}
	return gjson.GetBytes(doc, strings.Join(esc, "."))
}

func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(b.String(), "\n")
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --- building new text ------------------------------------------------------------------------

// buildArray renders a multi-line array whose KEY sits at nesting `level` (elements at level+1).
func buildArray(st style, level int, vals []string) string {
	if st.compact {
		return "[" + joinInline(st, vals) + "]"
	}
	var b strings.Builder
	b.WriteString("[" + st.eol)
	for i, v := range vals {
		b.WriteString(st.indent(level+1) + quote(v))
		if i < len(vals)-1 {
			b.WriteString(",")
		}
		b.WriteString(st.eol)
	}
	b.WriteString(st.indent(level) + "]")
	return b.String()
}

// buildBranch renders nested objects for keys[:len-1] ending in an array at keys[len-1].
// `level` is the nesting level of the member that will hold the returned value.
func buildBranch(st style, level int, keys []string, vals []string) string {
	if st.compact {
		if len(keys) == 1 {
			return "{" + quote(keys[0]) + st.colon + buildArray(st, level+1, vals) + "}"
		}
		return "{" + quote(keys[0]) + st.colon + buildBranch(st, level+1, keys[1:], vals) + "}"
	}
	if len(keys) == 1 {
		// keys[0] is the array key one level below; the array is the value of that member.
		return "{" + st.eol + st.indent(level+1) + quote(keys[0]) + st.colon + buildArray(st, level+1, vals) + st.eol + st.indent(level) + "}"
	}
	inner := buildBranch(st, level+1, keys[1:], vals)
	return "{" + st.eol + st.indent(level+1) + quote(keys[0]) + st.colon + inner + st.eol + st.indent(level) + "}"
}

// --- splicing -------------------------------------------------------------------------------

// objectSpan returns [open, close] byte offsets of the object at prefix (root when empty).
func objectSpan(doc []byte, prefix []string) (open, close int) {
	if len(prefix) == 0 {
		open = bytes.IndexFunc(doc, func(r rune) bool { return r != ' ' && r != '\t' && r != '\r' && r != '\n' })
		close = bytes.LastIndexByte(doc, '}')
		return open, close
	}
	r := lookup(doc, prefix)
	return r.Index, r.Index + len(r.Raw) - 1
}

// insertMember adds `"key": rawValue` as the last member of the object at prefix.
func insertMember(doc []byte, st style, prefix []string, key, rawValue string) ([]byte, error) {
	open, close := objectSpan(doc, prefix)
	if open < 0 || close <= open || doc[open] != '{' || doc[close] != '}' {
		return nil, fmt.Errorf("jsonedit: could not locate object %v", prefix)
	}
	level := len(prefix)
	if st.compact {
		return insertCompact(doc, st, open, close, quote(key)+st.colon+rawValue), nil
	}
	member := st.indent(level+1) + quote(key) + st.colon + rawValue

	// Last non-space byte before the closing brace.
	p := close - 1
	for p > open && isSpace(doc[p]) {
		p--
	}
	var b bytes.Buffer
	if p == open { // empty object
		b.Write(doc[:open+1])
		b.WriteString(st.eol + member + st.eol + st.indent(level))
		b.Write(doc[close:])
		return b.Bytes(), nil
	}
	b.Write(doc[:p+1])
	b.WriteString("," + st.eol + member)
	b.Write(doc[p+1:])
	return b.Bytes(), nil
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// appendToArray adds elements after the last element of an existing array, matching its layout.
// level is the nesting level of the array's key (elements sit at level+1).
func appendToArray(doc []byte, st style, level int, arr gjson.Result, vals []string) ([]byte, error) {
	open := arr.Index
	close := arr.Index + len(arr.Raw) - 1
	if doc[open] != '[' || doc[close] != ']' {
		return nil, fmt.Errorf("jsonedit: could not locate array")
	}
	inner := string(doc[open+1 : close])
	multiline := strings.Contains(inner, "\n")

	// Last non-space byte inside the brackets (empty array => p == open).
	p := close - 1
	for p > open && isSpace(doc[p]) {
		p--
	}
	var b bytes.Buffer
	switch {
	case p == open && !multiline: // []  or [ ]
		b.Write(doc[:open+1])
		b.WriteString(joinInline(st, vals))
		b.Write(doc[close:])
	case p == open: // [\n  \n]
		b.Write(doc[:open+1])
		for i, v := range vals {
			b.WriteString(st.eol + st.indent(level+1) + quote(v))
			if i < len(vals)-1 {
				b.WriteString(",")
			}
		}
		b.Write(doc[open+1:])
	case !multiline: // ["a","b"]
		b.Write(doc[:p+1])
		sep := ","
		if st.spaced {
			sep = ", "
		}
		for _, v := range vals {
			b.WriteString(sep + quote(v))
		}
		b.Write(doc[p+1:])
	default: // one element per line: reuse the last element's own indentation
		lineStart := bytes.LastIndexByte(doc[:p+1], '\n') + 1
		ind := leadingSpace(doc[lineStart:])
		b.Write(doc[:p+1])
		for _, v := range vals {
			b.WriteString("," + st.eol + ind + quote(v))
		}
		b.Write(doc[p+1:])
	}
	return b.Bytes(), nil
}

func joinInline(st style, vals []string) string {
	sep := ","
	if st.spaced {
		sep = ", "
	}
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = quote(v)
	}
	return strings.Join(q, sep)
}

func leadingSpace(b []byte) string {
	n := 0
	for n < len(b) && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return string(b[:n])
}

// insertCompact adds a member to a single-line object: {"a":1} -> {"a":1,"key":value}.
func insertCompact(doc []byte, st style, open, close int, member string) []byte {
	p := close - 1
	for p > open && isSpace(doc[p]) {
		p--
	}
	sep := ","
	if st.spaced {
		sep = ", "
	}
	var b bytes.Buffer
	if p == open { // {}
		b.Write(doc[:open+1])
		b.WriteString(member)
		b.Write(doc[close:])
		return b.Bytes()
	}
	b.Write(doc[:p+1])
	b.WriteString(sep + member)
	b.Write(doc[p+1:])
	return b.Bytes()
}

// ReadStrings returns the string elements of the array at path. A missing path yields (nil, nil);
// a value of the wrong type is an error.
func ReadStrings(doc []byte, path []string) ([]string, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil, nil
	}
	if !gjson.ValidBytes(doc) {
		return nil, ErrInvalidJSON
	}
	r := lookup(doc, path)
	if !r.Exists() {
		return nil, nil
	}
	if !r.IsArray() {
		return nil, fmt.Errorf("%w: %s is not an array", ErrWrongType, strings.Join(path, "."))
	}
	var out []string
	for _, e := range r.Array() {
		if e.Type != gjson.String {
			return nil, fmt.Errorf("%w: %s contains a non-string element", ErrWrongType, strings.Join(path, "."))
		}
		out = append(out, e.String())
	}
	return out, nil
}
