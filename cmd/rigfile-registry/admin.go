package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/rigfile/rigfile/internal/registry"
)

// operator is the identity recorded in the audit log for command-line actions (there is no account behind it).
var operator = &registry.User{Login: "operator", IsAdmin: true}

func admin(ctx context.Context, args []string, e env) int {
	if len(args) == 0 || args[0] == "help" {
		usage(e.out)
		return 0
	}
	cfg, err := loadConfig(e, false)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile-registry:", err)
		return 1
	}
	db, err := registry.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile-registry:", err)
		return 1
	}
	defer db.Close()
	st := registry.NewStore(db)
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(e.err)
	login := fs.String("login", "", "account login")
	ghID := fs.Int64("github-id", 0, "GitHub numeric user id")
	isAdmin := fs.Bool("admin", false, "make the account an administrator")
	name := fs.String("name", "cli", "token name")
	days := fs.Int("days", 30, "token lifetime in days")
	id := fs.Int64("id", 0, "report id")
	status := fs.String("status", "", "actioned or dismissed")
	rig := fs.String("rig", "", "owner/name")
	version := fs.String("version", "", "version (empty = the whole rig)")
	reason := fs.String("reason", "", "reason (recorded in the audit log)")
	limit := fs.Int("limit", 30, "how many entries")
	fs.String("kind", "person", "verification kind: person, organisation or domain")
	all := fs.Bool("all", false, "every account (revoke-tokens)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(e.err, "rigfile-registry:", err)
		return 1
	}
	switch args[0] {
	case "create-user":
		if *login == "" || *ghID == 0 {
			return fail(fmt.Errorf("create-user needs --login and --github-id"))
		}
		u, err := st.BootstrapUser(ctx, *ghID, *login, *isAdmin)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "account %s ready (admin=%v)\n", u.Login, u.IsAdmin)
	case "token":
		u, err := st.UserByLogin(ctx, *login)
		if err != nil {
			return fail(fmt.Errorf("no such account %q", *login))
		}
		tok, err := st.CreateToken(ctx, u.ID, *name, time.Duration(*days)*24*time.Hour)
		if err != nil {
			return fail(err)
		}
		st.Audit(ctx, operator, "admin.token", u.Login, map[string]any{"name": *name, "days": *days})
		fmt.Fprintln(e.out, tok) // shown once; only its hash is stored
	case "reports":
		rs, err := st.OpenReports(ctx)
		if err != nil {
			return fail(err)
		}
		if len(rs) == 0 {
			fmt.Fprintln(e.out, "no open reports")
		}
		for _, r := range rs {
			fmt.Fprintf(e.out, "#%d  %s  %s@%s  by %s  %s\n     %s\n", r.ID, r.CreatedAt.Format("2006-01-02"), r.RigRef, r.Version, r.Reporter, r.Reason, strings.ReplaceAll(r.Details, "\n", " "))
		}
	case "resolve-report":
		if err := st.ResolveReport(ctx, *id, *status, operator); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "report", *id, *status)
	case "takedown":
		owner, n, ok := strings.Cut(*rig, "/")
		if !ok || *reason == "" {
			return fail(fmt.Errorf("takedown needs --rig owner/name and --reason"))
		}
		if err := st.RemoveVersion(ctx, owner, n, *version, *reason, operator); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "removed", *rig, *version)
	case "verify-publisher":
		kind := fs.Lookup("kind").Value.String()
		if err := st.SetVerified(ctx, *login, kind, *reason, operator); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "verified", *login, "as", kind)
	case "unverify-publisher":
		if err := st.ClearVerified(ctx, *login, operator); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "verification removed from", *login)
	case "held":
		hs, err := st.HeldVersions(ctx)
		if err != nil {
			return fail(err)
		}
		if len(hs) == 0 {
			fmt.Fprintln(e.out, "nothing is held")
		}
		for _, h := range hs {
			fmt.Fprintf(e.out, "#%d  %s@%s  %s\n     %s\n", h.ID, h.Ref, h.Version, h.CreatedAt.Format("2006-01-02"), h.Reason)
		}
	case "release", "reject":
		if err := st.DecideHeld(ctx, *id, args[0] == "release", *reason, operator); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, args[0]+"d held version", *id)
	case "approve-public":
		owner, n, ok := strings.Cut(*rig, "/")
		if !ok {
			return fail(fmt.Errorf("approve-public needs --rig owner/name"))
		}
		if err := st.ApprovePublic(ctx, owner, n, operator); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, *rig, "approved to be public")
	case "publishing":
		if len(fs.Args()) == 0 {
			return fail(fmt.Errorf("publishing pause --reason R | publishing resume"))
		}
		sub := fs.Args()[0]
		if err := fs.Parse(fs.Args()[1:]); err != nil { // flags may follow the sub-command
			return 2
		}
		switch sub {
		case "pause":
			if err := st.PausePublishing(ctx, *reason, operator); err != nil {
				return fail(err)
			}
			fmt.Fprintln(e.out, "publishing paused")
		case "resume":
			if err := st.ResumePublishing(ctx, operator); err != nil {
				return fail(err)
			}
			fmt.Fprintln(e.out, "publishing resumed")
		default:
			return fail(fmt.Errorf("publishing pause | resume"))
		}
	case "revoke-tokens":
		if *login == "" && !*all {
			return fail(fmt.Errorf("revoke-tokens needs --login L or --all"))
		}
		who := *login
		if *all {
			who = ""
		}
		n, err := st.RevokeTokens(ctx, who, operator)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "revoked", n, "token(s)")
	case "disable-user", "enable-user":
		if err := st.SetDisabled(ctx, *login, args[0] == "disable-user"); err != nil {
			return fail(err)
		}
		st.Audit(ctx, operator, "admin."+args[0], *login, map[string]string{"reason": *reason})
		fmt.Fprintln(e.out, args[0], *login)
	case "disable-org", "enable-org":
		if err := st.SetOrgDisabled(ctx, *login, args[0] == "disable-org"); err != nil {
			return fail(err)
		}
		st.Audit(ctx, operator, "admin."+args[0], *login, map[string]string{"reason": *reason})
		fmt.Fprintln(e.out, args[0], *login)
	case "audit":
		es, err := st.RecentAudit(ctx, *limit)
		if err != nil {
			return fail(err)
		}
		for _, a := range es {
			fmt.Fprintf(e.out, "%s  %-10s %-20s %s  %s\n", a.At.Format("2006-01-02 15:04:05"), a.Actor, a.Action, a.Target, a.Detail)
		}
	default:
		usage(e.err)
		return 2
	}
	return 0
}
