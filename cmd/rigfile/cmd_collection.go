package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
)

const collectionUsage = `usage: rigfile collection <command> [--registry URL]

  create <slug> --title "..." [--description "..."] [--private]   make a collection
  add <slug> <owner/name> [--note "..."]                          add a rig (adding again changes the note)
  rm <slug> <owner/name>                                          take a rig out
  delete <slug>                                                   delete the collection
  show <owner/slug>                                               list what is in a collection
  list [<user>]                                                   list a user's collections (default: you)
`

func cmdCollection(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, collectionUsage)
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet("collection "+verb, flag.ContinueOnError)
	fs.SetOutput(e.err)
	registry := fs.String("registry", "", "Rigfile registry (default $RIGFILE_REGISTRY)")
	title := fs.String("title", "", "create: the collection's title")
	desc := fs.String("description", "", "create: a short description")
	private := fs.Bool("private", false, "create: only you can see it")
	note := fs.String("note", "", "add: why it is in the collection")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		return 2
	}
	base, err := registryBase(e, *registry)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	c := regClient(e, base)
	ctx := context.Background()
	fail := func(err error) int { fmt.Fprintln(e.err, "rigfile:", err); return 1 }
	usage := func() int { fmt.Fprint(e.err, collectionUsage); return 2 }
	me := func() (string, int) {
		login, err := c.Me(ctx)
		if err != nil {
			return "", fail(fmt.Errorf("not signed in (rigfile login): %w", err))
		}
		return login, 0
	}
	splitRig := func(s string) (string, string, bool) {
		o, n, ok := strings.Cut(s, "/")
		return o, n, ok && o != "" && n != ""
	}
	switch verb {
	case "create":
		if len(pos) != 1 || *title == "" {
			return usage()
		}
		vis := "public"
		if *private {
			vis = "private"
		}
		col, err := c.CreateCollection(ctx, pos[0], *title, *desc, vis)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Created %s/%s (%s): %s\n", col.Owner, col.Slug, col.Visibility, col.URL)
	case "add":
		if len(pos) != 2 {
			return usage()
		}
		login, code := me()
		if code != 0 {
			return code
		}
		if _, _, ok := splitRig(pos[1]); !ok {
			return usage()
		}
		if err := c.AddToCollection(ctx, login, pos[0], pos[1], *note); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Added %s to %s/%s.\n", pos[1], login, pos[0])
	case "rm":
		if len(pos) != 2 {
			return usage()
		}
		login, code := me()
		if code != 0 {
			return code
		}
		o, n, ok := splitRig(pos[1])
		if !ok {
			return usage()
		}
		if err := c.RemoveFromCollection(ctx, login, pos[0], o, n); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Removed %s from %s/%s.\n", pos[1], login, pos[0])
	case "delete":
		if len(pos) != 1 {
			return usage()
		}
		login, code := me()
		if code != 0 {
			return code
		}
		if err := c.DeleteCollection(ctx, login, pos[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Deleted %s/%s.\n", login, pos[0])
	case "show":
		if len(pos) != 1 {
			return usage()
		}
		o, s, ok := splitRig(pos[0])
		if !ok {
			return usage()
		}
		col, err := c.GetCollection(ctx, o, s)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "%s  (%s/%s, %s)\n", col.Title, col.Owner, col.Slug, col.Visibility)
		if col.Description != "" {
			fmt.Fprintln(e.out, col.Description)
		}
		for _, r := range col.Rigs {
			line := fmt.Sprintf("  %s/%s", r.Owner, r.Name)
			if r.Latest != "" {
				line += "@" + r.Latest
			}
			if r.Note != "" {
				line += "   " + r.Note
			}
			fmt.Fprintln(e.out, line)
		}
		if len(col.Rigs) == 0 {
			fmt.Fprintln(e.out, "  (empty)")
		}
	case "list":
		login := ""
		if len(pos) == 1 {
			login = pos[0]
		} else if len(pos) == 0 {
			var code int
			if login, code = me(); code != 0 {
				return code
			}
		} else {
			return usage()
		}
		cs, err := c.UserCollections(ctx, login)
		if err != nil {
			return fail(err)
		}
		for _, col := range cs {
			fmt.Fprintf(e.out, "%s/%s  %s  (%s)\n", col.Owner, col.Slug, col.Title, col.Visibility)
		}
		if len(cs) == 0 {
			fmt.Fprintln(e.out, "no collections")
		}
	default:
		return usage()
	}
	return 0
}
