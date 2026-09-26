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
	return appendGeneric(doc, path, vals, genericOps{
		render: func(_ style, _ int, v string) (string, error) { return quote(v), nil },
		existingKey: func(e gjson.Result) (string, error) {
			if e.Type != gjson.String {
				return "", fmt.Errorf("%w: array contains a non-string element", ErrWrongType)
			}
			return e.String(), nil
		},
		newKey: func(v string) (string, error) { return v, nil },
	})
}

// AppendRaw is AppendStrings for arbitrary JSON values (objects, arrays, numbers...). Each element of
// raws must be valid JSON. Elements are compared by their compacted text, so an element that is already
// present (byte-for-byte after compaction) is skipped. New elements are indented to match the document.
// Use it for structured entries such as Claude Code hook definitions.
func AppendRaw(doc []byte, path []string, raws []string) (out []byte, added []string, err error) {
	return appendGeneric(doc, path, raws, genericOps{
		render:      func(st style, level int, v string) (string, error) { return formatRaw(st, level, v) },
		existingKey: func(e gjson.Result) (string, error) { return compact(e.Raw) },
		newKey:      compact,
	})
}

// SetMissingString ensures the object member at path exists, giving it the string value if (and only if) it
// is absent. An existing member is NEVER changed: existing is its current value (raw JSON text for non-strings)
// and added is false. Missing parent objects are created. Layout of everything else is preserved.
func SetMissingString(doc []byte, path []string, value string) (out []byte, existing string, added bool, err error) {
	if len(path) == 0 {
		return nil, "", false, fmt.Errorf("jsonedit: empty path")
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		doc = []byte("{\n}\n")
	}
	if !gjson.ValidBytes(doc) {
		return nil, "", false, ErrInvalidJSON
	}
	if root := bytes.TrimSpace(doc); root[0] != '{' {
		return nil, "", false, fmt.Errorf("%w: root is not an object", ErrWrongType)
	}
	st := detectStyle(doc)
	prefix := []string{}
	for k := 0; k < len(path)-1; k++ {
		child := lookup(doc, append(prefix, path[k]))
		if !child.Exists() {
			raw := buildChain(st, len(prefix)+1, path[k+1:], quote(value))
			out, err := insertMember(doc, st, prefix, path[k], raw)
			return out, "", err == nil, err
		}
		if !child.IsObject() {
			return nil, "", false, fmt.Errorf("%w: %s is not an object", ErrWrongType, strings.Join(append(prefix, path[k]), "."))
		}
		prefix = append(prefix, path[k])
	}
	last := path[len(path)-1]
	if cur := lookup(doc, append(prefix, last)); cur.Exists() {
		if cur.Type == gjson.String {
			return doc, cur.String(), false, nil
		}
		return doc, cur.Raw, false, nil
	}
	out, err = insertMember(doc, st, prefix, last, quote(value))
	return out, "", err == nil, err
}

// ReadString returns the string value at path (ok=false when absent or not a string).
func ReadString(doc []byte, path []string) (string, bool) {
	if len(bytes.TrimSpace(doc)) == 0 || !gjson.ValidBytes(doc) {
		return "", false
	}
	r := lookup(doc, path)
	if !r.Exists() || r.Type != gjson.String {
		return "", false
	}
	return r.String(), true
}

// buildChain renders nested objects for keys, ending in the raw JSON leaf as the value of the last key.
// level is the nesting level of the member that will hold the returned value.
func buildChain(st style, level int, keys []string, rawLeaf string) string {
	if len(keys) == 0 {
		return rawLeaf
	}
	inner := buildChain(st, level+1, keys[1:], rawLeaf)
	if st.compact {
		return "{" + quote(keys[0]) + st.colon + inner + "}"
	}
	return "{" + st.eol + st.indent(level+1) + quote(keys[0]) + st.colon + inner + st.eol + st.indent(level) + "}"
}

// ReadRaw returns the compacted text of every element of the array at path (missing path: nil).
func ReadRaw(doc []byte, path []string) ([]string, error) {
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
		c, err := compact(e.Raw)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func compact(raw string) (string, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(raw)); err != nil {
		return "", fmt.Errorf("jsonedit: value is not valid JSON: %w", err)
	}
	return b.String(), nil
}

// formatRaw renders a JSON value for insertion as an array element at nesting `level`: compact for
// compact documents, otherwise indented with the document's own unit.
func formatRaw(st style, level int, raw string) (string, error) {
	c, err := compact(raw)
	if err != nil {
		return "", err
	}
	if st.compact {
		return c, nil
	}
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(c), st.indent(level), st.unit); err != nil {
		return "", err
	}
	// json.Indent applies the prefix to every line after the first (the caller places the first line)
	// and always emits "\n"; translate to the document's own line ending. A compacted JSON value
	// contains no literal newline (they are escaped inside strings), so this cannot alter content.
	return strings.ReplaceAll(b.String(), "\n", st.eol), nil
}

type genericOps struct {
	render      func(st style, level int, v string) (string, error) // element text for insertion
	existingKey func(e gjson.Result) (string, error)                // identity of an existing element
	newKey      func(v string) (string, error)                      // identity of a value to add
}

func appendGeneric(doc []byte, path []string, vals []string, ops genericOps) (out []byte, added []string, err error) {
	if len(path) == 0 {
		return nil, nil, fmt.Errorf("jsonedit: empty path")
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		doc = []byte("{\n}\n")
	}
	if !gjson.ValidBytes(doc) {
		return nil, nil, ErrInvalidJSON
	}
	if root := bytes.TrimSpace(doc); root[0] != '{' {
		return nil, nil, fmt.Errorf("%w: root is not an object", ErrWrongType)
	}
	st := detectStyle(doc)

	// de-duplicate the values to add (by identity), preserving order
	var uniq []string
	seenNew := map[string]bool{}
	for _, v := range vals {
		k, err := ops.newKey(v)
		if err != nil {
			return nil, nil, err
		}
		if !seenNew[k] {
			seenNew[k] = true
			uniq = append(uniq, v)
		}
	}
	renderAll := func(level int, vs []string) ([]string, error) {
		out := make([]string, len(vs))
		for i, v := range vs {
			r, err := ops.render(st, level, v)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	}

	// Walk down as far as the document already goes.
	prefix := []string{}
	for k := 0; k < len(path)-1; k++ {
		child := lookup(doc, append(prefix, path[k]))
		if !child.Exists() {
			// Missing from here on down: build the whole remaining branch and insert it as one member.
			if len(uniq) == 0 {
				return doc, nil, nil
			}
			// elements of the innermost array sit at level (len(prefix)+1) + (remaining keys) + 1
			elemLevel := len(prefix) + 1 + len(path[k+1:]) + 1
			rendered, err := renderAll(elemLevel, uniq)
			if err != nil {
				return nil, nil, err
			}
			raw := buildBranch(st, len(prefix)+1, path[k+1:], rendered)
			out, err := insertMember(doc, st, prefix, path[k], raw)
			return out, uniq, err
		}
		if !child.IsObject() {
			return nil, nil, fmt.Errorf("%w: %s is not an object", ErrWrongType, strings.Join(append(prefix, path[k]), "."))
		}
		prefix = append(prefix, path[k])
	}

	last := path[len(path)-1]
	arr := lookup(doc, append(prefix, last))
	if !arr.Exists() {
		if len(uniq) == 0 {
			return doc, nil, nil
		}
		rendered, err := renderAll(len(prefix)+2, uniq)
		if err != nil {
			return nil, nil, err
		}
		raw := buildArray(st, len(prefix)+1, rendered)
		out, err := insertMember(doc, st, prefix, last, raw)
		return out, uniq, err
	}
	if !arr.IsArray() {
		return nil, nil, fmt.Errorf("%w: %s is not an array", ErrWrongType, strings.Join(append(prefix, last), "."))
	}
	have := map[string]bool{}
	for _, e := range arr.Array() {
		k, err := ops.existingKey(e)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", strings.Join(append(prefix, last), "."), err)
		}
		have[k] = true
	}
	for _, v := range uniq {
		k, _ := ops.newKey(v)
		if !have[k] {
			added = append(added, v)
		}
	}
	if len(added) == 0 {
		return doc, nil, nil
	}
	rendered, err := renderAll(len(prefix)+2, added)
	if err != nil {
		return nil, nil, err
	}
	out, err = appendToArray(doc, st, len(prefix)+1, arr, rendered)
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
	hasMembers := bytes.Contains(doc, []byte(`":`))
	if (hasMembers && !bytes.Contains(doc, []byte(`": `))) || (!hasMembers && st.compact) {
		st.colon = ":" // members without a space, or an empty one-line document like {}
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
		b.WriteString(st.indent(level+1) + v)
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
			b.WriteString(st.eol + st.indent(level+1) + v)
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
			b.WriteString(sep + v)
		}
		b.Write(doc[p+1:])
	default: // one element per line: reuse the last element's own indentation
		lineStart := bytes.LastIndexByte(doc[:p+1], '\n') + 1
		ind := leadingSpace(doc[lineStart:])
		b.Write(doc[:p+1])
		for _, v := range vals {
			b.WriteString("," + st.eol + ind + v)
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
		q[i] = v
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

// RemoveRaw deletes every element of the array at path whose compacted JSON equals one of raws, by
// splicing text: the commas and whitespace around a removed element are removed with it and nothing
// else is touched. It is used only for entries Rigfile itself added (recorded in state.json).
// A missing path or no match is not an error. If the last element is removed the array becomes [].
func RemoveRaw(doc []byte, path []string, raws []string) (out []byte, removed int, err error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return doc, 0, nil
	}
	if !gjson.ValidBytes(doc) {
		return nil, 0, ErrInvalidJSON
	}
	want := map[string]bool{}
	for _, r := range raws {
		c, err := compact(r)
		if err != nil {
			return nil, 0, err
		}
		want[c] = true
	}
	out = doc
	// Removing shifts offsets, so re-locate the array after every removal.
	for {
		arr := lookup(out, path)
		if !arr.Exists() {
			return out, removed, nil
		}
		if !arr.IsArray() {
			return nil, 0, fmt.Errorf("%w: %s is not an array", ErrWrongType, strings.Join(path, "."))
		}
		spans := elementSpans(arr.Raw)
		hit := -1
		for i, sp := range spans {
			c, err := compact(arr.Raw[sp[0]:sp[1]])
			if err == nil && want[c] {
				hit = i
				break
			}
		}
		if hit < 0 {
			return out, removed, nil
		}
		base := arr.Index
		from, to := base+spans[hit][0], base+spans[hit][1]
		switch {
		case len(spans) == 1: // only element: empty the brackets
			from, to = base+1, base+len(arr.Raw)-1
		case hit < len(spans)-1: // remove through the start of the next element
			to = base + spans[hit+1][0]
		default: // last element: remove from the end of the previous one
			from = base + spans[hit-1][1]
		}
		var b bytes.Buffer
		b.Write(out[:from])
		b.Write(out[to:])
		out = b.Bytes()
		removed++
	}
}

// RemoveStrings is RemoveRaw for string values.
func RemoveStrings(doc []byte, path []string, vals []string) ([]byte, int, error) {
	raws := make([]string, len(vals))
	for i, v := range vals {
		raws[i] = quote(v)
	}
	return RemoveRaw(doc, path, raws)
}

// elementSpans returns [start,end) offsets (relative to raw, which must be a JSON array) of each
// top-level element, scanning strings and nesting properly.
func elementSpans(raw string) [][2]int {
	var spans [][2]int
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			if depth == 1 && start < 0 {
				start = i
			}
		case '[', '{':
			if depth == 1 && start < 0 {
				start = i
			}
			depth++
		case ']', '}':
			depth--
			if depth == 1 && start >= 0 { // closed a nested element
				spans = append(spans, [2]int{start, i + 1})
				start = -1
			}
			if depth == 0 && start >= 0 { // end of the array while a scalar element is open
				spans = append(spans, [2]int{start, trimEnd(raw, start, i)})
				start = -1
			}
		case ',':
			if depth == 1 && start >= 0 {
				spans = append(spans, [2]int{start, trimEnd(raw, start, i)})
				start = -1
			}
		case ' ', '\t', '\r', '\n':
		default:
			if depth == 1 && start < 0 {
				start = i
			}
		}
	}
	return spans
}

func trimEnd(raw string, start, end int) int {
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\t' || raw[end-1] == '\r' || raw[end-1] == '\n') {
		end--
	}
	return end
}
