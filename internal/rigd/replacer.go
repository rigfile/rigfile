package rigd

import (
	"bytes"
	"io"
)

// pair is one substitution.
type pair struct{ from, to []byte }

// streamReplacer replaces every occurrence of any `from` with its `to` in a stream, including occurrences that span read
// boundaries: at the end of what it has read it holds back only a tail that is a proper prefix of some `from` (which the
// next read may complete). It is used to scrub real secrets out of responses without buffering them whole.
type streamReplacer struct {
	src   io.Reader
	pairs []pair
	buf   []byte // read, not yet decided
	out   []byte // decided, ready to hand out
	eof   bool
	rerr  error
}

func newStreamReplacer(src io.Reader, pairs []pair) *streamReplacer {
	return &streamReplacer{src: src, pairs: pairs}
}

func (r *streamReplacer) prefixOfAny(rem []byte) bool {
	for _, p := range r.pairs {
		if len(p.from) > len(rem) && bytes.HasPrefix(p.from, rem) {
			return true
		}
	}
	return false
}

// drain decides as many bytes of buf as it can; final means no more input will come.
func (r *streamReplacer) drain(final bool) {
	i := 0
	for i < len(r.buf) {
		matched := false
		for _, p := range r.pairs {
			if len(p.from) > 0 && bytes.HasPrefix(r.buf[i:], p.from) {
				r.out = append(r.out, p.to...)
				i += len(p.from)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if !final && r.prefixOfAny(r.buf[i:]) {
			break // might be the start of a secret that the next read completes
		}
		r.out = append(r.out, r.buf[i])
		i++
	}
	r.buf = append([]byte(nil), r.buf[i:]...)
}

func (r *streamReplacer) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.eof {
			if r.rerr != nil && r.rerr != io.EOF {
				return 0, r.rerr
			}
			return 0, io.EOF
		}
		chunk := make([]byte, 32<<10)
		n, err := r.src.Read(chunk)
		r.buf = append(r.buf, chunk[:n]...)
		if err != nil {
			r.eof, r.rerr = true, err
		}
		r.drain(r.eof)
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}
