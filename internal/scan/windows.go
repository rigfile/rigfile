package scan

import (
	"sort"
	"strings"
)

// boundedRuleWindow lists rules whose regex is BOUNDED and whose every match contains one of the rule's
// keywords. For those, running the regex over the whole text is wasted work (on a 500 KB diff the
// generic-api-key rule alone costs ~110 ms), so we run it only on windows around keyword hits.
//
// Equivalence argument for generic-api-key: its pattern is `[\w.-]{0,50}?` + keyword + `[ \t\w.-]{0,20}` +
// `[\s'"]{0,3}` + operator + `[`'"\s=]{0,5}` + secret `{10,150}` + terminator, so a match is at most about
// 250 bytes and contains a keyword; a window of 512 bytes on each side, snapped outward to line boundaries,
// contains every match in full and never changes what `$`/`\s` see at its edges (a snapped edge is either
// the text end or a newline, which `\s` already accepts). TestWindowedMatchesEqualFullScan checks the
// equivalence on generated text. Do NOT add a rule here unless its regex is provably bounded.
var boundedRuleWindow = map[string]int{"generic-api-key": 512}

// findMatches returns the [start,end) offsets of every regex match in text.
func findMatches(r *rule, text, lower string) [][]int {
	if r.window == 0 || len(r.keywords) == 0 {
		return r.re.FindAllStringIndex(text, -1)
	}
	var spans [][2]int
	for _, k := range r.keywords {
		for from := 0; from < len(lower); {
			i := strings.Index(lower[from:], k)
			if i < 0 {
				break
			}
			pos := from + i
			a, b := pos-r.window, pos+len(k)+r.window
			if a < 0 {
				a = 0
			}
			if b > len(text) {
				b = len(text)
			}
			// snap outward to line boundaries
			if a > 0 {
				if j := strings.LastIndexByte(text[:a], '\n'); j >= 0 {
					a = j + 1
				} else {
					a = 0
				}
			}
			if b < len(text) {
				if j := strings.IndexByte(text[b:], '\n'); j >= 0 {
					b += j + 1
				} else {
					b = len(text)
				}
			}
			spans = append(spans, [2]int{a, b})
			from = pos + len(k)
		}
	}
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
	merged := spans[:1]
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp[0] <= last[1] {
			if sp[1] > last[1] {
				last[1] = sp[1]
			}
		} else {
			merged = append(merged, sp)
		}
	}
	var out [][]int
	for _, sp := range merged {
		for _, m := range r.re.FindAllStringIndex(text[sp[0]:sp[1]], -1) {
			out = append(out, []int{m[0] + sp[0], m[1] + sp[0]})
		}
	}
	return out
}
