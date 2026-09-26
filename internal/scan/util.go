package scan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"sort"
	"strings"
)

func anyMatch(res []*regexp.Regexp, s string) bool {
	if s == "" {
		return false
	}
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// stopwordCoverage is the share of a secret's letters/digits that stopwords explain. gitleaks suppresses a
// finding when the secret merely CONTAINS any stopword, and the generic rule's list has ~1,400 dictionary
// words, so a random 25-character key that happens to contain "meta" is silently dropped (found by the
// S2-M2 corpus). Rigfile suppresses only when stopwords account for at least this share of the secret, which
// still removes word-built identifiers ("authorization_backend_configuration") but keeps random secrets.
const stopwordCoverage = 0.6

// placeholderMarkers are stopwords that say "this is not a real credential" wherever they appear in the
// secret, so they suppress on contain (like gitleaks) instead of by coverage: "...EXAMPLEKEY", "change-me",
// "xxxxx". They are long enough that a random secret will not contain one by chance.
var placeholderMarkers = []string{"example", "sample", "placeholder", "changeme", "change-me", "change_me", "redacted", "dummy", "xxxxx",
	"replaceme", "replace-me", "replace_me", "yourkey", "your-key", "your_key", "yourtoken", "your-token", "your_token", "yoursecret", "your-secret",
	"your_secret", "fixme"}

func containsStop(words []string, secret string) bool {
	if len(words) == 0 || secret == "" {
		return false
	}
	l := strings.ToLower(secret)
	for _, m := range placeholderMarkers {
		if strings.Contains(l, m) {
			for _, w := range words {
				if w == m {
					return true
				}
			}
		}
	}
	covered := make([]bool, len(l))
	any := false
	for _, w := range words {
		for from := 0; from < len(l); {
			i := strings.Index(l[from:], w)
			if i < 0 {
				break
			}
			any = true
			for k := from + i; k < from+i+len(w); k++ {
				covered[k] = true
			}
			from += i + 1
		}
	}
	if !any {
		return false
	}
	total, hit := 0, 0
	for i := 0; i < len(l); i++ {
		c := l[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c >= 128 {
			total++
			if covered[i] {
				hit++
			}
		}
	}
	return total > 0 && float64(hit) >= stopwordCoverage*float64(total)
}

func fingerprint(rule, path, secret string) string {
	h := sha256.Sum256([]byte(rule + "\x00" + path + "\x00" + secret))
	return hex.EncodeToString(h[:])[:12]
}

// isBinary uses git's heuristic: a NUL byte in the first 8000 bytes.
func isBinary(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// shannonEntropy is gitleaks' definition (detect/utils.go): frequency over runes, normalised by BYTE length.
func shannonEntropy(data string) (entropy float64) {
	if data == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, c := range data {
		counts[c]++
	}
	inv := 1.0 / float64(len(data))
	for _, n := range counts {
		f := float64(n) * inv
		entropy -= f * math.Log2(f)
	}
	return entropy
}

// lineIndex maps byte offsets to 1-based lines.
type lineIndex struct{ starts []int } // starts[i] = offset of line i+1

func newLineIndex(text string) lineIndex {
	st := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			st = append(st, i+1)
		}
	}
	return lineIndex{st}
}

// locate returns the start line, end line and 1-based start column of [start,end).
func (l lineIndex) locate(start, end int) (startLine, endLine, col int) {
	i := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > start }) - 1
	startLine = i + 1
	col = start - l.starts[i] + 1
	if end <= start {
		return startLine, startLine, col
	}
	j := sort.Search(len(l.starts), func(k int) bool { return l.starts[k] > end-1 }) - 1
	return startLine, j + 1, col
}

// text returns the source lines [startLine,endLine] without the trailing newline.
func (l lineIndex) text(text string, startLine, endLine int) string {
	a := l.starts[startLine-1]
	b := len(text)
	if endLine < len(l.starts) {
		b = l.starts[endLine] - 1
	}
	if b < a {
		b = a
	}
	return strings.TrimRight(text[a:b], "\r")
}

// asciiLower lower-cases A-Z only, so byte offsets stay identical to the input (strings.ToLower can change
// the length of non-ASCII text, which would misalign keyword positions).
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
