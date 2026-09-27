package main

import (
	"context"
	"flag"
	"fmt"
)

const orgUsage = `usage: rigfile org <command> [--registry URL]

  create <name> [--title "..."]          make an organisation; you become its owner
  list                                   the organisations you belong to
  members <org>                          who is in it (members only)
  add <org> <user> [--role member|admin|owner]   add someone who has signed in to the registry, or change their role
  rm <org> <user>                        remove someone (or yourself: leave)

Publish under an organisation by naming the rig <org>/<name>; every member may publish and yank, owners and admins
manage members, and only owners and admins change a rig's visibility.
`

func cmdOrg(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, orgUsage)
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet("org "+verb, flag.ContinueOnError)
	fs.SetOutput(e.err)
	registry := fs.String("registry", "", "Rigfile registry (default $RIGFILE_REGISTRY)")
	title := fs.String("title", "", "create: the organisation's display name")
	role := fs.String("role", "member", "add: member, admin or owner")
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
	usage := func() int { fmt.Fprint(e.err, orgUsage); return 2 }
	switch verb {
	case "create":
		if len(pos) != 1 {
			return usage()
		}
		if err := c.CreateOrg(ctx, pos[0], *title); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Created the organisation %s; you are its owner. Publish under it as %s/<rig>.\n", pos[0], pos[0])
	case "list":
		orgs, err := c.MyOrgs(ctx)
		if err != nil {
			return fail(err)
		}
		for _, o := range orgs {
			fmt.Fprintf(e.out, "%s  (%s)\n", o.Login, o.Role)
		}
		if len(orgs) == 0 {
			fmt.Fprintln(e.out, "you are not in any organisation")
		}
	case "members":
		if len(pos) != 1 {
			return usage()
		}
		ms, err := c.OrgMembers(ctx, pos[0])
		if err != nil {
			return fail(err)
		}
		for _, m := range ms {
			fmt.Fprintf(e.out, "%s  (%s)\n", m.Login, m.Role)
		}
	case "add":
		if len(pos) != 2 {
			return usage()
		}
		if err := c.SetOrgMember(ctx, pos[0], pos[1], *role); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "%s is now %s of %s.\n", pos[1], map[string]string{"member": "a member", "admin": "an admin", "owner": "an owner"}[*role], pos[0])
	case "rm":
		if len(pos) != 2 {
			return usage()
		}
		if err := c.RemoveOrgMember(ctx, pos[0], pos[1]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "%s is no longer in %s.\n", pos[1], pos[0])
	default:
		return usage()
	}
	return 0
}
