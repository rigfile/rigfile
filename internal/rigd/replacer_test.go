package rigd

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestStreamReplacerFindsMatchesAcrossReadBoundaries(t *testing.T) {
	from := "REAL" + "-" + "value" + "-" + "1234"
	pairs := []pair{{[]byte(from), []byte("rgs_sur_X")}}
	in := "aa" + from + "bb" + from[:5] + "cc" + strings.Repeat("z", 50) + from
	want := "aargs_sur_Xbb" + from[:5] + "cc" + strings.Repeat("z", 50) + "rgs_sur_X"
	for name, src := range map[string]io.Reader{
		"whole":    strings.NewReader(in),
		"one byte": iotest.OneByteReader(strings.NewReader(in)),
		"half":     iotest.HalfReader(strings.NewReader(in)),
		"data+eof": iotest.DataErrReader(strings.NewReader(in)),
	} {
		b, err := io.ReadAll(newStreamReplacer(src, pairs))
		if err != nil || string(b) != want {
			t.Errorf("%s: %q err=%v", name, b, err)
		}
	}
	// a stream that ends inside a partial match releases the held-back bytes
	b, _ := io.ReadAll(newStreamReplacer(strings.NewReader("x"+from[:6]), pairs))
	if string(b) != "x"+from[:6] {
		t.Fatalf("%q", b)
	}
}
