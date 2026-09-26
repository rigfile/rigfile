package publish

import (
	"fmt"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
)

// readme describes what is inside a published rig, how to pull it, and what it will ask for. It is generated from
// the manifest so it cannot claim more than the rig contains.
func readme(m *manifest.Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", m.Name)
	if m.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", m.Description)
	}
	owner, name, _ := strings.Cut(m.Name, "/")
	fmt.Fprintf(&b, "A [Rigfile](https://github.com/digitaldreamer3462/rigfile) rig, version %s.\n\n## Use it\n\n```sh\nrigfile pull github.com/%s/%s     # fetch, show the plan, apply on approval\n```\n\n", m.Version, owner, name)
	b.WriteString("**Review before you approve.** A rig can install hooks and scripts and register MCP servers; those run on your machine. `rigfile pull` shows every one of them first and changes nothing until you approve.\n\n## What is inside\n\n")
	list := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		sort.Strings(items)
		fmt.Fprintf(&b, "**%s**\n\n", title)
		for _, i := range items {
			fmt.Fprintf(&b, "- %s\n", i)
		}
		b.WriteString("\n")
	}
	var x []string
	for _, i := range m.Instructions {
		x = append(x, "`"+i.ID+"` ("+i.EffectiveScope()+")")
	}
	list("Instructions", x)
	x = nil
	for _, i := range m.Skills {
		x = append(x, "`"+i.Key()+"`")
	}
	list("Skills", x)
	x = nil
	for _, i := range m.Agents {
		x = append(x, "`"+i.Key()+"`")
	}
	list("Agents", x)
	x = nil
	for _, i := range m.Commands {
		x = append(x, "`/"+i.Key()+"`")
	}
	list("Commands", x)
	x = nil
	for n, s := range m.MCPServers {
		switch {
		case s.URL != "":
			x = append(x, fmt.Sprintf("`%s`: remote `%s`", n, s.URL))
		default:
			x = append(x, fmt.Sprintf("`%s`: runs `%s %s`", n, s.Command, strings.Join(s.Args, " ")))
		}
	}
	list("MCP servers (they execute code)", x)
	x = nil
	for _, h := range m.Hooks {
		x = append(x, fmt.Sprintf("`%s` on `%s` (executes code)", h.ID, h.Event))
	}
	list("Hooks", x)
	var need []string
	for ref, s := range m.Secrets {
		line := "`" + ref + "`"
		if s.Description != "" {
			line += ": " + s.Description
		}
		if s.ObtainURL != "" {
			line += " (" + s.ObtainURL + ")"
		}
		need = append(need, line)
	}
	list("Secrets you will be asked for (values are never in this repository)", need)
	need = nil
	for _, l := range m.Logins {
		line := "`" + l.Provider + "`"
		if l.Reason != "" {
			line += ": " + l.Reason
		}
		need = append(need, line)
	}
	list("Logins", need)
	b.WriteString("`rigfile/base-secure` (secret protection for the agent and for git) is always applied underneath and cannot be removed by a rig.\n")
	return b.String()
}
