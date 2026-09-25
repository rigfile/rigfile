// Package splice edits text files by inserting, replacing and removing marker-delimited regions
// that Rigfile owns. Everything outside a region is preserved byte-for-byte (this is the
// "marker-splice" strategy of ADR 0001 §6: no round-tripping parser is needed, so user comments,
// ordering and whitespace can't be lost).
//
// Marker format (docs/merge-semantics.md §4.1):
//
//	# rigfile:begin <id> sha256=<12 hex of the canonical body>      (Hash style: TOML, YAML, sh)
//	...owned content...
//	# rigfile:end <id>
//
//	<!-- rigfile:begin <id> sha256=... -->                          (HTML style: Markdown)
//	...owned content...
//	<!-- rigfile:end <id> -->
//
// Security notes (threat note for this package): bodies come from rigs, which are untrusted input.
// A body may not contain the marker text at all (it could otherwise forge or terminate a region),
// ids are restricted to a safe alphabet, and an edited region is refused (ErrDrift) rather than
// silently overwritten.
package splice

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrDrift means the region's body no longer matches the hash in its begin marker: someone
	// edited Rigfile-managed text. Pass Options.Overwrite to replace it anyway.
	ErrDrift = errors.New("splice: managed region was edited since it was written")
	// ErrMalformed means markers are unbalanced, nested, duplicated or misordered.
	ErrMalformed = errors.New("splice: malformed or ambiguous markers")
	// ErrBadInput means the id or body is not acceptable.
	ErrBadInput = errors.New("splice: invalid id or body")
)

// Options tune Upsert.
type Options struct {
	// Overwrite replaces a region even if it was edited since it was written.
	Overwrite bool
}

// Style describes a comment syntax.
type Style struct {
	open, close string
	begin, end  *regexp.Regexp
}

func newStyle(open, close string) Style {
	q := regexp.QuoteMeta
	o := q(strings.TrimSpace(open))
	c := q(strings.TrimSpace(close))
	return Style{
		open:  open,
		close: close,
		begin: regexp.MustCompile(`^\s*` + o + `\s*rigfile:begin\s+([A-Za-z0-9_.#/-]+)(?:\s+sha256=([0-9a-f]{12}))?\s*` + c + `\s*$`),
		end:   regexp.MustCompile(`^\s*` + o + `\s*rigfile:end\s+([A-Za-z0-9_.#/-]+)\s*` + c + `\s*$`),
	}
}

var (
	// Hash is `# ...` comments (TOML, YAML, shell).
	Hash = newStyle("# ", "")
	// HTML is `<!-- ... -->` comments (Markdown).
	HTML = newStyle("<!-- ", " -->")
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9_.#/-]{1,128}$`)

// Region is a located managed region.
type Region struct {
	ID      string
	Body    []byte // content between the markers, with the file's own line endings
	Hash    string // hash recorded in the begin marker ("" if absent)
	Drifted bool   // recorded hash does not match the current body

	begin, end int // line indexes of the begin/end marker lines
}

// Find locates region id. found is false if it is absent.
func Find(doc []byte, st Style, id string) (Region, bool, error) {
	if !idRe.MatchString(id) {
		return Region{}, false, fmt.Errorf("%w: id %q", ErrBadInput, id)
	}
	lines := splitLines(doc)
	regs, err := scan(lines, st)
	if err != nil {
		return Region{}, false, err
	}
	var hit []Region
	for _, r := range regs {
		if r.ID == id {
			hit = append(hit, r)
		}
	}
	switch len(hit) {
	case 0:
		return Region{}, false, nil
	case 1:
		return hit[0], true, nil
	default:
		return Region{}, false, fmt.Errorf("%w: region %q appears %d times", ErrMalformed, id, len(hit))
	}
}

// Upsert inserts region id with body, or replaces its body in place (keeping its position).
// changed is false when the file already contains exactly this region.
func Upsert(doc []byte, st Style, id string, body []byte, opt Options) (out []byte, changed bool, err error) {
	if !idRe.MatchString(id) {
		return nil, false, fmt.Errorf("%w: id %q", ErrBadInput, id)
	}
	if bytes.Contains(body, []byte("rigfile:begin")) || bytes.Contains(body, []byte("rigfile:end")) {
		return nil, false, fmt.Errorf("%w: body must not contain marker text", ErrBadInput)
	}
	eol := detectEOL(doc)
	canon := canonical(body)
	sum := shortHash(canon)

	r, found, err := Find(doc, st, id)
	if err != nil {
		return nil, false, err
	}
	block := renderBlock(st, id, sum, canon, eol)

	if !found {
		return appendBlock(doc, block, eol), true, nil
	}
	if r.Hash != "" && r.Drifted && !opt.Overwrite {
		return nil, false, fmt.Errorf("%w: %q", ErrDrift, id)
	}
	if r.Hash == sum && !r.Drifted {
		return doc, false, nil
	}
	lines := splitLines(doc)
	var b bytes.Buffer
	for _, l := range lines[:r.begin] {
		b.Write(l)
	}
	b.WriteString(block)
	for _, l := range lines[r.end+1:] {
		b.Write(l)
	}
	return b.Bytes(), true, nil
}

// Remove deletes region id (and the single blank separator line Upsert added before it, if the
// region is the last thing in the file). removed is false if the region was absent.
func Remove(doc []byte, st Style, id string) (out []byte, removed bool, err error) {
	r, found, err := Find(doc, st, id)
	if err != nil || !found {
		return doc, false, err
	}
	lines := splitLines(doc)
	start := r.begin
	if r.end == len(lines)-1 && start > 0 && strings.TrimSpace(string(lines[start-1])) == "" {
		start-- // drop our separator only when the region was at EOF
	}
	var b bytes.Buffer
	for _, l := range lines[:start] {
		b.Write(l)
	}
	for _, l := range lines[r.end+1:] {
		b.Write(l)
	}
	return b.Bytes(), true, nil
}

// --- internals -------------------------------------------------------------------------------

func splitLines(doc []byte) [][]byte {
	if len(doc) == 0 {
		return nil
	}
	parts := bytes.SplitAfter(doc, []byte("\n"))
	if len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func detectEOL(doc []byte) string {
	if i := bytes.IndexByte(doc, '\n'); i > 0 && doc[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func trimEOL(l []byte) string { return strings.TrimRight(string(l), "\r\n") }

// canonical returns the body with LF endings and every line terminated ("" for an empty body).
func canonical(body []byte) string {
	s := strings.ReplaceAll(string(body), "\r\n", "\n")
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

func shortHash(canon string) string {
	h := sha256.Sum256([]byte(canon))
	return hex.EncodeToString(h[:])[:12]
}

func renderBlock(st Style, id, sum, canon, eol string) string {
	var b strings.Builder
	b.WriteString(st.open + "rigfile:begin " + id + " sha256=" + sum + st.close + eol)
	if canon != "" {
		b.WriteString(strings.ReplaceAll(canon, "\n", eol))
	}
	b.WriteString(st.open + "rigfile:end " + id + st.close + eol)
	return b.String()
}

func appendBlock(doc []byte, block, eol string) []byte {
	var b bytes.Buffer
	b.Write(doc)
	if len(doc) > 0 {
		if !bytes.HasSuffix(doc, []byte("\n")) {
			b.WriteString(eol)
		}
		// Always exactly one blank separator line, even if the file already ends in a blank line:
		// Remove drops exactly one, so Upsert+Remove restores the original bytes.
		b.WriteString(eol)
	}
	b.WriteString(block)
	return b.Bytes()
}

// scan finds all regions and rejects unbalanced, nested or misordered markers.
func scan(lines [][]byte, st Style) ([]Region, error) {
	var regs []Region
	open := -1
	openID, openHash := "", ""
	for i, l := range lines {
		s := trimEOL(l)
		if m := st.begin.FindStringSubmatch(s); m != nil {
			if open >= 0 {
				return nil, fmt.Errorf("%w: begin %q inside open region %q (line %d)", ErrMalformed, m[1], openID, i+1)
			}
			open, openID, openHash = i, m[1], m[2]
			continue
		}
		if m := st.end.FindStringSubmatch(s); m != nil {
			if open < 0 {
				return nil, fmt.Errorf("%w: end %q without begin (line %d)", ErrMalformed, m[1], i+1)
			}
			if m[1] != openID {
				return nil, fmt.Errorf("%w: end %q closes region %q (line %d)", ErrMalformed, m[1], openID, i+1)
			}
			var body bytes.Buffer
			for _, bl := range lines[open+1 : i] {
				body.Write(bl)
			}
			r := Region{ID: openID, Body: body.Bytes(), Hash: openHash, begin: open, end: i}
			r.Drifted = openHash != "" && shortHash(canonical(body.Bytes())) != openHash
			regs = append(regs, r)
			open = -1
		}
	}
	if open >= 0 {
		return nil, fmt.Errorf("%w: region %q is never closed", ErrMalformed, openID)
	}
	return regs, nil
}

// StripAll returns doc without any Rigfile-managed regions (markers included) and the ids removed.
// Text outside the regions is untouched, except that the blank separator left behind is collapsed.
func StripAll(doc []byte, st Style) ([]byte, []string, error) {
	lines := splitLines(doc)
	regs, err := scan(lines, st)
	if err != nil {
		return nil, nil, err
	}
	if len(regs) == 0 {
		return doc, nil, nil
	}
	drop := map[int]bool{}
	var ids []string
	for _, r := range regs {
		ids = append(ids, r.ID)
		for i := r.begin; i <= r.end; i++ {
			drop[i] = true
		}
	}
	var out []byte
	blank := false
	for i, l := range lines {
		if drop[i] {
			continue
		}
		if len(bytes.TrimSpace(l)) == 0 {
			if blank || len(out) == 0 {
				continue // collapse runs of blank lines and drop leading ones
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l...)
	}
	return bytes.TrimRight(out, "\r\n \t"), ids, nil
}
