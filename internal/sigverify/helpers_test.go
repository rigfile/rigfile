package sigverify

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func mustHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
