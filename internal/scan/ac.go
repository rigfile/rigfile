package scan

// A small Aho-Corasick automaton: one pass over the (lower-cased) text tells us which rule keywords occur,
// instead of one strings.Contains per keyword (hundreds of passes over the whole diff). Keywords are ASCII
// (checked when building); any byte >= 0x80 resets the automaton, which is correct because no keyword
// contains one.

type acDFA struct {
	next [][128]int32
	out  [][]int32 // keyword indexes ending at each state (including via fail links)
	n    int       // number of keywords
}

func buildAC(words []string) *acDFA {
	a := &acDFA{n: len(words)}
	a.next = append(a.next, [128]int32{})
	a.out = append(a.out, nil)
	for i, w := range words {
		s := int32(0)
		for j := 0; j < len(w); j++ {
			c := w[j] & 0x7f
			if a.next[s][c] == 0 {
				a.next = append(a.next, [128]int32{})
				a.out = append(a.out, nil)
				a.next[s][c] = int32(len(a.next) - 1)
			}
			s = a.next[s][c]
		}
		a.out[s] = append(a.out[s], int32(i))
	}
	// BFS: fail links and completed transitions
	fail := make([]int32, len(a.next))
	var queue []int32
	for c := 0; c < 128; c++ {
		if t := a.next[0][c]; t != 0 {
			queue = append(queue, t)
		}
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		a.out[s] = append(a.out[s], a.out[fail[s]]...)
		for c := 0; c < 128; c++ {
			t := a.next[s][c]
			if t == 0 {
				a.next[s][c] = a.next[fail[s]][c]
				continue
			}
			fail[t] = a.next[fail[s]][c]
			queue = append(queue, t)
		}
	}
	return a
}

// present reports, for every keyword index, whether it occurs in text (already lower-cased).
func (a *acDFA) present(text string) []bool {
	seen := make([]bool, a.n)
	s := int32(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 128 {
			s = 0
			continue
		}
		s = a.next[s][c]
		if o := a.out[s]; len(o) > 0 {
			for _, k := range o {
				seen[k] = true
			}
		}
	}
	return seen
}
