package registry

import (
	"io/fs"
	"strings"
	"testing"
)

// Every page in the sidebar has a Markdown file, and every Markdown file is reachable from the sidebar.
func TestDocsNavAndFilesMatch(t *testing.T) {
	inNav := map[string]bool{}
	for _, p := range docPages {
		if inNav[p.Slug] {
			t.Errorf("duplicate docs slug %q", p.Slug)
		}
		inNav[p.Slug] = true
		if _, err := docsFS.ReadFile("web/docs/" + p.Slug + ".md"); err != nil {
			t.Errorf("docs page %q has no web/docs/%s.md", p.Slug, p.Slug)
		}
		if p.Title == "" || p.Group == "" || p.Summary == "" {
			t.Errorf("docs page %q needs a title, group and summary", p.Slug)
		}
	}
	ents, err := fs.ReadDir(docsFS, "web/docs")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if slug := strings.TrimSuffix(e.Name(), ".md"); !inNav[slug] {
			t.Errorf("web/docs/%s is not in docPages (unreachable)", e.Name())
		}
	}
}

// Internal /docs/ links inside the docs point at pages that exist.
func TestDocsInternalLinksResolve(t *testing.T) {
	exists := map[string]bool{}
	for _, p := range docPages {
		exists[p.Slug] = true
	}
	for _, p := range docPages {
		b, _ := docsFS.ReadFile("web/docs/" + p.Slug + ".md")
		for _, part := range strings.Split(string(b), "](/docs/")[1:] {
			slug := part[:strings.IndexAny(part, ")#")]
			if !exists[slug] {
				t.Errorf("%s.md links to /docs/%s, which does not exist", p.Slug, slug)
			}
		}
	}
}

// The supported-tools page is built from the adapters' capability files: every tool has a column and a card.
func TestToolsTableCoversEveryCapabilityFile(t *testing.T) {
	tt, err := buildToolsTable()
	if err != nil {
		t.Fatal(err)
	}
	titles := supportedToolTitles()
	if len(titles) < 2 || len(tt.Tools) != len(titles) {
		t.Fatalf("tools: %d cards for %d titles", len(tt.Tools), len(titles))
	}
	if titles[0] != "Claude Code" {
		t.Errorf("Claude Code should come first, got %q", titles[0])
	}
	for _, row := range tt.Rows {
		if len(row.Cells) != len(titles) {
			t.Errorf("row %q has %d cells for %d tools", row.Category, len(row.Cells), len(titles))
		}
		for _, c := range row.Cells {
			if c.Mark == "" {
				t.Errorf("row %q: support %q has no mark", row.Category, c.Support)
			}
		}
	}
}

// The CSP (style-src 'self') silently drops inline style attributes, so templates must never use them.
func TestTemplatesHaveNoInlineStyles(t *testing.T) {
	err := fs.WalkDir(templateFS, "web/templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templateFS.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), " style=") || strings.Contains(string(b), "<style") || strings.Contains(string(b), "<script") {
			t.Errorf("%s: inline style or script (blocked by the CSP)", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithoutTitleDropsOnlyTheMatchingHeading(t *testing.T) {
	if got := withoutTitle("# jia/demo\n\nhello\n", "jia/demo"); strings.Contains(got, "# jia/demo") || !strings.Contains(got, "hello") {
		t.Errorf("heading not dropped: %q", got)
	}
	if got := withoutTitle("# Something else\nhello", "jia/demo"); !strings.HasPrefix(got, "# Something else") {
		t.Errorf("an unrelated heading was dropped: %q", got)
	}
}
