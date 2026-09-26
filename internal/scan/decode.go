package scan

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
)

// Encoded secrets. gitleaks decodes base64, hex and percent-encoded segments and scans the result, because
// hiding a token in `payload: Z2hwX...` defeats a plain scan (found by the S2-M2 corpus: 0/10 before this).
// We do the same with tight limits so a large diff stays fast and binary blobs (images, hashes) are ignored:
// a decoded segment is scanned only if it is mostly printable text.

const (
	maxDecodeDepth    = 2        // base64 inside base64 inside text
	maxDecodeSegments = 256      // per scanned text
	maxSegmentBytes   = 64 << 10 // per candidate segment
	maxDecodedTotal   = 1 << 20  // decoded bytes examined per scanned text
)

// Candidate runs are found with byte loops, not regexps: over a 500 KB diff three regexps cost ~60 ms.

func isB64Byte(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '_' || c == '-'
}

func isAlnum(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isPctByte: URL characters plus '%'.
func isPctByte(c byte) bool {
	return isAlnum(c) || strings.IndexByte("._~:/?#@!$&'()*+,;=%-", c) >= 0
}

// runs returns the [start,end) of every maximal run of bytes satisfying in(c) with length >= min.
func runs(text string, min int, in func(byte) bool) [][2]int {
	var out [][2]int
	for i := 0; i < len(text); {
		if !in(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && in(text[j]) {
			j++
		}
		if j-i >= min {
			out = append(out, [2]int{i, j})
		}
		i = j
	}
	return out
}

func (s *Scanner) scanDecoded(name, text string, lines lineIndex, depth int) []Finding {
	var out []Finding
	budget := maxDecodedTotal
	segs := 0
	try := func(start, end int, decoded string) {
		if segs >= maxDecodeSegments || budget <= 0 || !mostlyText(decoded) {
			return
		}
		segs++
		budget -= len(decoded)
		startLine, endLine, col := lines.locate(start, end)
		for _, f := range s.scanText(name, decoded, depth+1) {
			f.Description = "(decoded) " + f.Description
			f.Line, f.End, f.Column = startLine, endLine, col
			out = append(out, f)
		}
	}

	for _, m := range runs(text, 24, isB64Byte) {
		end := m[1]
		for k := 0; k < 2 && end < len(text) && text[end] == '='; k++ {
			end++
		}
		seg := text[m[0]:end]
		if len(seg) > maxSegmentBytes || !looksLikeBase64(seg) {
			continue
		}
		if d, ok := decodeBase64(seg); ok {
			try(m[0], end, d)
		}
		if segs >= maxDecodeSegments {
			return out
		}
	}
	for _, m := range runs(text, 32, isAlnum) {
		seg := text[m[0]:m[1]]
		if len(seg)%2 != 0 || len(seg) > maxSegmentBytes || !allHex(seg) {
			continue
		}
		if b, err := hex.DecodeString(seg); err == nil {
			try(m[0], m[1], string(b))
		}
		if segs >= maxDecodeSegments {
			return out
		}
	}
	if strings.IndexByte(text, '%') >= 0 {
		for _, m := range runs(text, 12, isPctByte) {
			seg := text[m[0]:m[1]]
			if len(seg) > maxSegmentBytes || countPct(seg) < 2 {
				continue
			}
			if d, err := url.PathUnescape(seg); err == nil && d != seg {
				try(m[0], m[1], d)
			}
			if segs >= maxDecodeSegments {
				return out
			}
		}
	}
	return out
}

func allHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHexByte(s[i]) {
			return false
		}
	}
	return true
}

// countPct counts %XX escapes.
func countPct(s string) int {
	n := 0
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '%' && isHexByte(s[i+1]) && isHexByte(s[i+2]) {
			n++
		}
	}
	return n
}

// looksLikeBase64 filters identifier-shaped candidates (snake_case words, kebab-case) before decoding.
func looksLikeBase64(s string) bool {
	var upper, lower, digit, sym bool
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		case c == '+' || c == '/' || c == '=':
			sym = true
		}
	}
	n := 0
	for _, b := range []bool{upper, lower, digit, sym} {
		if b {
			n++
		}
	}
	return n >= 2
}

func decodeBase64(seg string) (string, bool) {
	seg = strings.TrimRight(seg, "=")
	enc := base64.RawStdEncoding
	if strings.ContainsAny(seg, "-_") {
		enc = base64.RawURLEncoding
	}
	// a stray trailing character makes the length invalid; drop up to 3 to find a decodable prefix
	for i := 0; i < 4 && len(seg) > 16; i++ {
		if b, err := enc.DecodeString(seg[:len(seg)-i]); err == nil {
			return string(b), true
		}
	}
	return "", false
}

// mostlyText reports whether at least 90% of the bytes are printable ASCII or common whitespace, and the
// text is long enough to hold a secret. Random bytes (hashes, images, compressed data) fail this.
func mostlyText(d string) bool {
	if len(d) < 12 {
		return false
	}
	ok := 0
	for i := 0; i < len(d); i++ {
		c := d[i]
		if (c >= 0x20 && c < 0x7f) || c == '\n' || c == '\r' || c == '\t' {
			ok++
		}
	}
	return ok*10 >= len(d)*9
}
