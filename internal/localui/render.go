package localui

import (
	"html"
	"html/template"
	"strconv"
	"strings"
)

// This file turns the plain-text transcript that `rigfile plan`/`apply` prints (internal/engine.Plan.Render,
// plus the surrounding lines cmd_rig.go's planApply adds) into readable, sectioned HTML for the web page. The
// terminal output itself is untouched: this is a presentational layer only, built defensively so a format it
// does not recognise still renders — as plain, fully escaped text — rather than losing or corrupting anything.
//
// Every branch below escapes raw text with html.EscapeString before it is embedded; only hand-written, constant
// strings are ever written unescaped. That is the one invariant every change to this file must keep, since the
// text can come from a rig's own (untrusted) file names and error messages (see docs/local-ui.md).

// opSymbols mirrors internal/engine's Op symbols, one CSS class per meaning.
var opSymbols = map[byte]string{'+': "op-new", '~': "op-update", '=': "op-unchanged", '!': "op-conflict", '-': "op-removal"}

// transcript is registered as a template func: {{.Plan | transcript}}, {{.Result | transcript}}.
func transcript(text string) template.HTML {
	var b strings.Builder
	for _, block := range splitBlocks(text) {
		lines := strings.Split(block, "\n")
		if len(lines) == 0 {
			continue
		}
		b.WriteString(`<section class="tblock"><h3>`)
		b.WriteString(html.EscapeString(lines[0]))
		b.WriteString("</h3>")
		renderRows(&b, lines[1:])
		b.WriteString("</section>")
	}
	return template.HTML(b.String())
}

// splitBlocks splits on blank lines, dropping empty blocks (the CLI's own blank-line separators between
// sections, e.g. between one target's plan and the next).
func splitBlocks(text string) []string {
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		cur = append(cur, l)
	}
	flush()
	return blocks
}

// renderRows renders one block's body (everything after its heading line).
func renderRows(b *strings.Builder, lines []string) {
	b.WriteString(`<div class="rows">`)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "NOT APPLICABLE HERE":
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], " ") {
				j++
			}
			n := j - (i + 1)
			b.WriteString(`<details class="noise"><summary>Not applicable here (`)
			b.WriteString(strconv.Itoa(n))
			b.WriteString(` item`)
			if n != 1 {
				b.WriteString("s")
			}
			b.WriteString(`)</summary>`)
			for k := i + 1; k < j; k++ {
				writeMuted(b, lines[k])
			}
			b.WriteString(`</details>`)
			i = j - 1
		case strings.HasPrefix(line, "NOTE") && (len(line) == 4 || line[4] == ' '):
			b.WriteString(`<div class="note">`)
			b.WriteString(html.EscapeString(strings.TrimSpace(strings.TrimPrefix(line, "NOTE"))))
			b.WriteString(`</div>`)
		default:
			if label, sym, rest, ok := parseOpRow(line); ok {
				writeOpRow(b, label, sym, rest)
			} else if strings.HasPrefix(line, " ") {
				writeMuted(b, line)
			} else {
				b.WriteString(`<div class="ln">`)
				b.WriteString(html.EscapeString(line))
				b.WriteString(`</div>`)
			}
		}
	}
	b.WriteString(`</div>`)
}

// parseOpRow recognises internal/engine.Plan.Render's exact row format: fmt.Sprintf("%-13s %s %s%s\n", title,
// symbol, summary, warn) for a new row, or the same with an empty title for a continuation. Byte positions are
// fixed by that Sprintf, not guessed: label = line[0:13], a literal space, the one-byte symbol, a literal
// space, then the rest of the line.
func parseOpRow(line string) (label string, symbol byte, rest string, ok bool) {
	if len(line) < 16 || line[13] != ' ' || line[15] != ' ' {
		return "", 0, "", false
	}
	if _, known := opSymbols[line[14]]; !known {
		return "", 0, "", false
	}
	return strings.TrimRight(line[:13], " "), line[14], line[16:], true
}

func writeOpRow(b *strings.Builder, label string, sym byte, rest string) {
	b.WriteString(`<div class="row">`)
	if label != "" {
		b.WriteString(`<span class="cat">`)
		b.WriteString(html.EscapeString(label))
		b.WriteString(`</span>`)
	} else {
		b.WriteString(`<span class="cat"></span>`)
	}
	b.WriteString(`<span class="sym `)
	b.WriteString(opSymbols[sym])
	b.WriteString(`">`)
	b.WriteString(html.EscapeString(string(sym)))
	b.WriteString(`</span><span class="txt">`)
	b.WriteString(html.EscapeString(rest))
	b.WriteString(`</span></div>`)
}

// writeMuted renders an indented (continuation/detail, or collapsed-noise) line: same content, quieter style,
// its own leading whitespace stripped since CSS indentation replaces it.
func writeMuted(b *strings.Builder, line string) {
	b.WriteString(`<div class="detail">`)
	b.WriteString(html.EscapeString(strings.TrimLeft(line, " ")))
	b.WriteString(`</div>`)
}
