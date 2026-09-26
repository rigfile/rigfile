// Package similar finds rig names that could be mistaken for one another (typosquatting and look-alikes,
// docs/trust.md §4). It is a warning system: a match is shown to the publisher and to the person about to pull, and only
// the strongest kind of match against a notable rig sends a request for review.
package similar

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// confusable maps characters that look like a Latin letter or digit to a canonical one. The list is deliberately short:
// the common Cyrillic and Greek look-alikes, and the digits and letters people swap on purpose.
var confusable = map[rune]rune{
	'0': 'o', '1': 'l', 'i': 'l', '|': 'l', '5': 's', '$': 's', '3': 'e', '4': 'a', '@': 'a', '7': 't', 'ı': 'l',
	// Cyrillic
	'а': 'a', 'в': 'b', 'е': 'e', 'к': 'k', 'м': 'm', 'н': 'h', 'о': 'o', 'р': 'p', 'с': 'c', 'т': 't', 'у': 'y', 'х': 'x', 'і': 'l', 'ј': 'j', 'ѕ': 's',
	// Greek
	'α': 'a', 'β': 'b', 'ε': 'e', 'ι': 'l', 'κ': 'k', 'ν': 'v', 'ο': 'o', 'ρ': 'p', 'τ': 't', 'υ': 'u', 'χ': 'x', 'ω': 'w',
}

// Normalize reduces a name to a comparison form: NFKC, lower case, separators removed, confusable characters folded, and
// the two-letter look-alikes "rn"→"m" and "vv"→"w". It is only ever used for comparing, never for storing.
func Normalize(name string) string {
	name = strings.ToLower(norm.NFKC.String(name))
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '-', '_', '.', ' ':
			continue
		}
		if c, ok := confusable[r]; ok {
			r = c
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	s := b.String()
	s = strings.ReplaceAll(s, "rn", "m")
	s = strings.ReplaceAll(s, "vv", "w")
	return s
}

// Distance is the optimal-string-alignment (Damerau-Levenshtein) distance between two strings.
func Distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				if t := prev2[j-2] + 1; t < v {
					v = t
				}
			}
			cur[j] = v
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// Candidate is an existing public rig.
type Candidate struct {
	Owner, Name string
	Stars       int
	Verified    bool // the publisher is a verified publisher
}

// Kinds of match, strongest first.
const (
	Lookalike = "lookalike" // identical after normalisation but not written the same
	Affix     = "affix"     // the same name plus or minus a word such as -official, -pro or -v2
	Close     = "close"     // a small edit away
)

// Match is one similar rig.
type Match struct {
	Ref      string
	Kind     string
	Stars    int
	Verified bool
}

// Notable reports whether a match is worth interrupting someone for.
func (m Match) Notable(minStars int) bool { return m.Verified || m.Stars >= minStars }

// Strong reports whether the match is the kind that should ask for review before the name goes public.
func (m Match) Strong() bool { return m.Kind == Lookalike || m.Kind == Affix }

var affixes = []string{"official", "pro", "plus", "new", "secure", "fixed", "latest", "real", "original", "v2", "v3", "2", "3", "ai", "tools", "kit", "setup"}

var normAffixes = func() []string {
	out := make([]string, len(affixes))
	for i, a := range affixes {
		out[i] = Normalize(a)
	}
	return out
}()

func hasAffix(short, long string) bool {
	if short == long || len(long) <= len(short) || len(short) < 5 {
		return false
	}
	for _, a := range normAffixes {
		if long == short+a || long == a+short {
			return true
		}
	}
	return false
}

// Find compares owner/name with existing rigs. A rig by the same owner is never a match (people make v2s of their own
// work), and identical names under different owners are legitimate and only reported when the other is notable.
func Find(owner, name string, existing []Candidate) []Match {
	n := Normalize(name)
	if n == "" {
		return nil
	}
	var out []Match
	for _, c := range existing {
		if strings.EqualFold(c.Owner, owner) {
			continue
		}
		cn := Normalize(c.Name)
		if cn == "" {
			continue
		}
		ref := c.Owner + "/" + c.Name
		switch {
		case strings.EqualFold(c.Name, name):
			out = append(out, Match{Ref: ref, Kind: "same-name", Stars: c.Stars, Verified: c.Verified})
		case n == cn:
			out = append(out, Match{Ref: ref, Kind: Lookalike, Stars: c.Stars, Verified: c.Verified})
		case hasAffix(n, cn) || hasAffix(cn, n):
			out = append(out, Match{Ref: ref, Kind: Affix, Stars: c.Stars, Verified: c.Verified})
		default:
			limit := 1
			if len(n) >= 8 && len(cn) >= 8 {
				limit = 2
			}
			if len(n) >= 5 && len(cn) >= 5 && Distance(n, cn) <= limit {
				out = append(out, Match{Ref: ref, Kind: Close, Stars: c.Stars, Verified: c.Verified})
			}
		}
	}
	return out
}
