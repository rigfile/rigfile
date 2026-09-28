package registry

import (
	"embed"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/rigfile/rigfile/internal/targets"
)

//go:embed web/docs/*.md
var docsFS embed.FS

// docPage is one page of the user documentation. The order of docPages is the order of the sidebar, the index and
// the previous/next links.
type docPage struct {
	Slug, Title, Group, Summary string
}

var docPages = []docPage{
	{"getting-started", "Getting started", "Start", "Install the CLI, capture your setup, apply it and share it, in five minutes."},
	{"concepts", "Concepts", "Start", "Rigs, layers, targets, the plan, the lockfile and base-secure."},
	{"writing-a-rig", "Writing a rig", "Guides", "A complete, annotated rigfile.yaml, section by section."},
	{"secrets", "Secrets", "Guides", "secret:// references, the keychain, rigfile exec and the broker."},
	{"publishing", "Publishing and sharing", "Guides", "Publish to git or this registry: scrubbing, pinning, scanning, visibility."},
	{"pulling", "Pulling a rig safely", "Guides", "Sources, the plan screen, trust facts, updates and rollback."},
	{"collaboration", "Forks, collections and organisations", "Guides", "Build on someone's rig, curate lists, publish as a team."},
	{"local-models", "Local models", "Guides", "Run a local model per machine, verified and pinned."},
	{"sync", "Sync between your machines", "Guides", "End-to-end encrypted sync of your own private files."},
	{"manifest", "Manifest reference", "Reference", "Every field of rigfile.yaml."},
	{"cli", "CLI reference", "Reference", "Every rigfile command, with examples."},
	{"tools", "Supported tools", "Reference", "What Rigfile can configure in each AI tool, generated from the adapters."},
	{"security", "Security model and limits", "Reference", "What Rigfile protects, how, and what it does not."},
	{"faq", "FAQ and troubleshooting", "Reference", "Common questions and the errors people actually hit."},
}

type docGroup struct {
	Name  string
	Pages []docPage
}

// toolsTable is the supported-tools page, built from the same capability files the CLI adapters use, so it cannot
// drift from what `rigfile plan` actually does.
type toolsTable struct {
	Tools []toolInfo
	Rows  []toolRow
}

type toolInfo struct {
	Title, BaseSecure, BaseSecureLevel string
	OS                                 []string
	Categories                         []toolCell
	Paths                              []string
}

type toolRow struct {
	Category string
	Cells    []toolCell
}

type toolCell struct {
	Category, Support, Mark, Note string
}

type docsData struct {
	Groups     []docGroup
	Page       docPage
	Index      bool
	Body       template.HTML
	Prev, Next *docPage
	Tools      *toolsTable
}

func docGroups() []docGroup {
	var gs []docGroup
	for _, p := range docPages {
		if len(gs) == 0 || gs[len(gs)-1].Name != p.Group {
			gs = append(gs, docGroup{Name: p.Group})
		}
		gs[len(gs)-1].Pages = append(gs[len(gs)-1].Pages, p)
	}
	return gs
}

func (s *Server) pageDocsIndex(w http.ResponseWriter, r *http.Request) {
	_, u, csrf := s.pageViewer(r)
	s.render(w, r, http.StatusOK, "docs.html", Page{Title: "Documentation", User: u, CSRF: csrf,
		Data: docsData{Groups: docGroups(), Index: true, Page: docPage{Title: "Documentation"}}})
}

func (s *Server) pageDocs(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("page")
	i := -1
	for j, p := range docPages {
		if p.Slug == slug {
			i = j
		}
	}
	if i < 0 {
		s.notFound(w, r)
		return
	}
	b, err := docsFS.ReadFile("web/docs/" + slug + ".md")
	if err != nil {
		s.notFound(w, r)
		return
	}
	// %REGISTRY% in the Markdown becomes this registry's own address, so examples are copy-and-paste correct on
	// every deployment (staging included).
	src := strings.ReplaceAll(string(b), "%REGISTRY%", strings.TrimRight(s.Cfg.PublicURL, "/"))
	d := docsData{Groups: docGroups(), Page: docPages[i], Body: renderMarkdown(src)}
	if i > 0 {
		d.Prev = &docPages[i-1]
	}
	if i+1 < len(docPages) {
		d.Next = &docPages[i+1]
	}
	if slug == "tools" {
		t, err := buildToolsTable()
		if err != nil {
			s.Log.Error("capabilities", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		d.Tools = t
	}
	_, u, csrf := s.pageViewer(r)
	s.render(w, r, http.StatusOK, "docs.html", Page{Title: docPages[i].Title, Desc: docPages[i].Summary, User: u, CSRF: csrf, Data: d})
}

var (
	supportMark     = map[string]string{"yes": "✔", "partial": "◐", "no": "✘"}
	baseSecureLabel = map[string]string{"enforced": "enforced (permissions + hooks)", "partial": "partly enforced, rest as instructions",
		"instructions_only": "instructions only, not enforced", "none": "not applicable"}
	baseSecureLevel = map[string]string{"enforced": "ok", "partial": "warn", "instructions_only": "warn", "none": ""}
)

// orderedCapabilities returns the capability files in the CLI's order: Claude Code first, then by name.
func orderedCapabilities() ([]targets.Capabilities, error) {
	caps, err := targets.LoadCapabilities()
	if err != nil {
		return nil, err
	}
	out := make([]targets.Capabilities, 0, len(caps))
	for _, c := range caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target == "claude-code" || out[j].Target == "claude-code" {
			return out[i].Target == "claude-code"
		}
		return out[i].Target < out[j].Target
	})
	return out, nil
}

// supportedToolTitles is the list of tool names shown on the home page.
func supportedToolTitles() []string {
	cs, err := orderedCapabilities()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Title)
	}
	return out
}

func buildToolsTable() (*toolsTable, error) {
	cs, err := orderedCapabilities()
	if err != nil {
		return nil, err
	}
	t := &toolsTable{}
	for _, cat := range targets.CategoryOrder {
		row := toolRow{Category: strings.ReplaceAll(cat, "_", " ")}
		for _, c := range cs {
			x := c.Categories[cat]
			row.Cells = append(row.Cells, toolCell{Support: x.Support, Mark: supportMark[x.Support], Note: x.Note})
		}
		t.Rows = append(t.Rows, row)
	}
	for _, c := range cs {
		ti := toolInfo{Title: c.Title, OS: c.OS, BaseSecure: baseSecureLabel[c.BaseSecure], BaseSecureLevel: baseSecureLevel[c.BaseSecure]}
		for _, cat := range targets.CategoryOrder {
			x := c.Categories[cat]
			ti.Categories = append(ti.Categories, toolCell{Category: strings.ReplaceAll(cat, "_", " "), Support: x.Support, Mark: supportMark[x.Support], Note: x.Note})
		}
		for _, o := range []string{"macos", "linux", "windows"} {
			if p, ok := c.ConfigPaths[o]; ok {
				ti.Paths = append(ti.Paths, o+": "+p)
			}
		}
		t.Tools = append(t.Tools, ti)
	}
	return t, nil
}
