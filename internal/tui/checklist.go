// Package tui is the small terminal interface Rigfile needs: a keyboard checklist. It works over any reader and
// writer, so tests drive it with scripted key presses; the caller puts a real terminal in raw mode.
package tui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Item is one checkbox.
type Item struct {
	Group   string
	Label   string
	Detail  string // shown dimly after the label (where it came from)
	Warn    string // shown under the item when set (why it starts unticked)
	Checked bool
}

// Options tune the rendering.
type Options struct {
	Title string
	Help  string
	ANSI  bool // redraw in place with cursor movement; false prints the list again after each key
}

// Checklist lets the user tick and untick items and returns the final selection. ok is false when the user
// cancelled (q, Ctrl-C, Esc, or end of input). Keys: up/down or k/j move, space toggles, a selects all, n none,
// Enter confirms.
func Checklist(in io.Reader, out io.Writer, items []Item, o Options) (sel []bool, ok bool, err error) {
	sel = make([]bool, len(items))
	for i, it := range items {
		sel[i] = it.Checked
	}
	if len(items) == 0 {
		return sel, true, nil
	}
	r := bufio.NewReader(in)
	cur := 0
	lines := 0
	draw := func() {
		if o.ANSI && lines > 0 {
			fmt.Fprintf(out, "\x1b[%dA\x1b[J", lines)
		}
		lines = render(out, items, sel, cur, o)
	}
	draw()
	for {
		b, err := r.ReadByte()
		if err != nil {
			return sel, false, nil
		}
		switch b {
		case 3, 'q', 'Q': // Ctrl-C, q
			return sel, false, nil
		case '\r', '\n':
			if !o.ANSI {
				fmt.Fprintln(out)
			}
			return sel, true, nil
		case ' ':
			sel[cur] = !sel[cur]
		case 'a', 'A':
			for i := range sel {
				sel[i] = true
			}
		case 'n', 'N':
			for i := range sel {
				sel[i] = false
			}
		case 'k', 'K':
			cur = (cur + len(items) - 1) % len(items)
		case 'j', 'J':
			cur = (cur + 1) % len(items)
		case 0x1b: // escape sequence: arrows are ESC [ A / ESC [ B; a lone Esc cancels
			if r.Buffered() == 0 {
				return sel, false, nil
			}
			b2, _ := r.ReadByte()
			if b2 != '[' && b2 != 'O' {
				return sel, false, nil
			}
			b3, _ := r.ReadByte()
			switch b3 {
			case 'A':
				cur = (cur + len(items) - 1) % len(items)
			case 'B':
				cur = (cur + 1) % len(items)
			}
		}
		draw()
	}
}

func render(out io.Writer, items []Item, sel []bool, cur int, o Options) int {
	n := 0
	line := func(format string, a ...any) {
		fmt.Fprintf(out, format+"\n", a...)
		n++
	}
	if o.Title != "" {
		line("%s", o.Title)
	}
	group := ""
	for i, it := range items {
		if it.Group != group {
			group = it.Group
			line("")
			line("%s", strings.ToUpper(group))
		}
		mark := " "
		if sel[i] {
			mark = "x"
		}
		pointer := "  "
		if i == cur {
			pointer = "> "
		}
		s := fmt.Sprintf("%s[%s] %s", pointer, mark, it.Label)
		if it.Detail != "" {
			s += "   " + it.Detail
		}
		line("%s", s)
		if it.Warn != "" {
			line("        ! %s", it.Warn)
		}
	}
	line("")
	help := o.Help
	if help == "" {
		help = "up/down move   space toggle   a all   n none   enter confirm   q cancel"
	}
	line("%s", help)
	return n
}
