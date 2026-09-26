package registry

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// README, manifests and file contents are attacker-controlled text. Markdown is rendered with raw HTML DISABLED
// (goldmark's default), dangerous URL schemes are dropped by goldmark, and the result is passed through bluemonday's
// user-generated-content policy as a second, independent layer. The CSP (no script, images only from this site and
// GitHub avatars) is the third.
var (
	mdRenderer = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithHardWraps()), // note: no html.WithUnsafe()
	)
	mdPolicy = func() *bluemonday.Policy {
		p := bluemonday.UGCPolicy()
		p.AllowURLSchemes("http", "https", "mailto")
		p.RequireNoFollowOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		p.RequireNoReferrerOnLinks(true)
		return p
	}()
)

// renderMarkdown turns untrusted Markdown into safe HTML.
func renderMarkdown(src string) template.HTML {
	var b bytes.Buffer
	if len(src) > 256<<10 {
		src = src[:256<<10]
	}
	if err := mdRenderer.Convert([]byte(src), &b); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(mdPolicy.SanitizeBytes(b.Bytes())) //nolint:gosec // sanitized above
}
