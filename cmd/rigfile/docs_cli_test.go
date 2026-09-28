package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The registry's CLI reference (internal/registry/web/docs/cli.md) documents every top-level command in the usage
// text, so a new command cannot ship without a line on the website.
func TestWebsiteCLIReferenceCoversEveryCommand(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "internal", "registry", "web", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	usage(&b)
	n := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(strings.TrimSpace(line), "-") {
			continue
		}
		fields := strings.Fields(line)
		cmds := []string{fields[0]}
		if len(fields) > 4 && fields[1] == "|" { // "login | logout | whoami"
			cmds = []string{fields[0], fields[2], fields[4]}
		}
		for _, c := range cmds {
			n++
			if !strings.Contains(string(doc), "`rigfile "+c+"`") {
				t.Errorf("cli.md does not document `rigfile %s`", c)
			}
		}
	}
	if n < 20 {
		t.Fatalf("parsed only %d commands from the usage text; the parser is out of date", n)
	}
}
