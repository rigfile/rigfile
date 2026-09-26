package similar

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Data-Science": "datascience", "data_science": "datascience", "data.science": "datascience",
		"d4ta-sc1ence": "datascience", "dat@-$cience": "datascience", // digit/symbol swaps
		"rnodel": "model", "vvork": "work", // two-letter look-alikes
		"dаta-science": "datascience", // Cyrillic а
		"ｄａｔａ":         "data",        // full-width forms
		"":             "", "---": "",
	} {
		// the 1 → l fold makes "sc1ence" → "sclence"; normalise both sides the same way
		if got := Normalize(in); got != Normalize(want) {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, Normalize(want))
		}
	}
}

func TestDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		d    int
	}{{"", "", 0}, {"a", "", 1}, {"kitten", "sitting", 3}, {"ab", "ba", 1}, {"datascience", "datasience", 1}, {"same", "same", 0}} {
		if got := Distance(c.a, c.b); got != c.d {
			t.Errorf("Distance(%q,%q) = %d, want %d", c.a, c.b, got, c.d)
		}
	}
}

func kinds(ms []Match) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		out[m.Ref] = m.Kind
	}
	return out
}

func TestSimilarNames(t *testing.T) {
	popular := []Candidate{
		{Owner: "jiaxu", Name: "data-science", Stars: 340, Verified: true},
		{Owner: "acme", Name: "python-dev", Stars: 20},
		{Owner: "zed", Name: "web-tools", Stars: 0},
		{Owner: "me", Name: "my-own", Stars: 3},
	}
	cases := []struct {
		owner, name string
		want        map[string]string
	}{
		{"evil", "data-sciense", map[string]string{"jiaxu/data-science": Close}},
		{"evil", "datascience", map[string]string{"jiaxu/data-science": Lookalike}},
		{"evil", "data_science", map[string]string{"jiaxu/data-science": Lookalike}},
		{"evil", "d4ta-science", map[string]string{"jiaxu/data-science": Lookalike}},
		{"evil", "data-science-official", map[string]string{"jiaxu/data-science": Affix}},
		{"evil", "data-science", map[string]string{"jiaxu/data-science": "same-name"}},
		{"evil", "python-dev", map[string]string{"acme/python-dev": "same-name"}},
		{"evil", "pythom-dev", map[string]string{"acme/python-dev": Close}},
		{"me", "my-own", map[string]string{}},         // your own rigs never match
		{"me", "my-own-v2", map[string]string{}},      // ... including your own v2
		{"evil", "kitchen-sink", map[string]string{}}, // unrelated
		{"evil", "web", map[string]string{}},          // too short for distance matching
	}
	for _, c := range cases {
		got := kinds(Find(c.owner, c.name, popular))
		if len(got) != len(c.want) {
			t.Errorf("Find(%s/%s) = %v, want %v", c.owner, c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("Find(%s/%s)[%s] = %q, want %q", c.owner, c.name, k, got[k], v)
			}
		}
	}
	// notability and strength
	m := Find("evil", "datascience", popular)[0]
	if !m.Strong() || !m.Notable(5) {
		t.Fatalf("%+v", m)
	}
	quiet := Match{Ref: "zed/web-tools", Kind: Close}
	if quiet.Notable(5) || quiet.Strong() {
		t.Fatal("a rig with no stars is not notable")
	}
	if !(Match{Kind: Close, Stars: 6}).Notable(5) {
		t.Fatal("stars make a match notable")
	}
}
