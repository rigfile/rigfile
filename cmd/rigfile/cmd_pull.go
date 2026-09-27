package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/source"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// cmdPull implements `pull <source>` and `update`: fetch a rig from a git source into the content-addressed cache,
// then run the normal review-and-apply flow on it, with the source, commit and content hash on the screen.
func cmdPull(verb string, args []string, e env) int {
	var f rigFlags
	fs := rigFlagSet(verb, e, &f, true)
	planOnly := fs.Bool("plan-only", false, "show the plan for the fetched rig and stop (nothing is applied)")
	requireSig := fs.Bool("require-signature", false, "refuse a registry rig unless its Sigstore signature verifies here and is the publisher's own GitHub Actions identity")
	showDiff := fs.Bool("diff", false, "update: also show the text of changed files")
	acceptSigner := fs.Bool("accept-signer-change", false, "update: accept a version whose signer differs from the one you pulled before")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	client := sourceClient(e, sd)

	var spec source.Spec
	var prev *state.RigRef
	switch verb {
	case "pull":
		if len(pos) != 1 {
			fmt.Fprintln(e.err, "usage: rigfile pull <github.com/owner/repo[@ref][//dir] | gitlab.com/... | https://host/repo.git[@ref]> [flags]")
			return 2
		}
		arg := pos[0]
		switch {
		case source.Looks(arg):
			if spec, err = source.Parse(arg); err != nil {
				fmt.Fprintln(e.err, "rigfile:", err)
				return 2
			}
		case registryRef.MatchString(arg) && !isDir(arg):
			base, err := registryBase(e, f.registry)
			if err != nil {
				fmt.Fprintln(e.err, "rigfile:", err)
				return 1
			}
			name, ref, _ := strings.Cut(arg, "@")
			spec = source.Spec{Kind: source.Registry, URL: base, Path: name, Ref: ref}
		default:
			fmt.Fprintf(e.err, "rigfile: %q is not a git source or a registry rig (owner/name). To apply a rig directory on this machine use `rigfile apply %s`.\n", arg, arg)
			return 2
		}
	case "update":
		if len(pos) != 0 {
			fmt.Fprintln(e.err, "usage: rigfile update [flags]")
			return 2
		}
		st, err := state.Load(sd)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		for _, at := range appliedTargets(st) {
			if at.TS.Rig.Source != "" {
				r := at.TS.Rig
				prev = &r
				break
			}
		}
		if prev == nil {
			fmt.Fprintln(e.err, "rigfile: nothing was pulled from a git source on this machine; use `rigfile pull <source>` first")
			return 1
		}
		if spec, err = source.Parse(prev.Source); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
	}

	got, err := client.Get(context.Background(), spec, nil)
	if err != nil {
		if errors.Is(err, source.ErrChanged) {
			fmt.Fprintln(e.err, "rigfile: the source no longer matches what was pinned:", err)
		} else {
			fmt.Fprintln(e.err, "rigfile:", err)
		}
		return 1
	}
	if prev != nil && prev.Commit == got.Commit && prev.TreeSHA256 == got.TreeSHA256 {
		fmt.Fprintf(e.out, "%s is up to date (commit %s)\n", spec, got.Commit[:12])
		return 0
	}

	if spec.Kind == source.Registry {
		if code := checkSignature(e, got, prev, *requireSig, *acceptSigner); code != 0 {
			return code
		}
	}
	origin := "a git repository"
	if spec.Kind == source.Registry {
		origin = "the Rigfile registry (" + spec.URL + ")"
	}
	banner := []string{
		fmt.Sprintf("Source: %s   commit %s   tree %s", spec, got.Commit[:12], got.TreeSHA256[:12]),
		"!!! This rig comes from " + origin + " and you did not write it. Review every hook, script and MCP command below:",
		"!!! approving the plan lets them run on this machine. Nothing has run yet.",
	}
	if got.Yanked {
		banner = append(banner, "!!! The publisher YANKED this version: "+got.YankReason)
	}
	if spec.Kind == source.Registry {
		banner = append(banner, trustLines(e, spec, got)...)
	}
	if prev != nil {
		head := []string{fmt.Sprintf("Update: %s -> %s", short12(prev.Commit), short12(got.Commit))}
		head = append(head, updateChangeLines(prev.Dir, got.Dir, short12(prev.Commit), short12(got.Commit), *showDiff)...)
		banner = append(head, banner...)
	}
	if spec.Ref == "" || !source.IsCommit(spec.Ref) && prev == nil {
		banner = append(banner, fmt.Sprintf("note: %s was resolved to commit %s now and is pinned to it; `rigfile update` follows the ref later.", refName(spec), got.Commit[:12]))
	}
	if spec.Kind == source.Registry {
		banner = append(banner, signatureLines(got)...)
	}
	f.pulled = &pulledRig{Source: spec.String(), Commit: got.Commit, Tree: got.TreeSHA256, Signer: got.Signer.String(), Banner: banner}
	mode := "apply"
	if *planOnly {
		mode = "plan"
	}
	return planApply(mode, got.Dir, f, e)
}

var registryRef = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}/[a-z0-9][a-z0-9._-]{0,62}(@[\^~]?[0-9A-Za-z][0-9A-Za-z._+-]*)?$`)

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func refName(s source.Spec) string {
	if s.Ref == "" {
		return "the default branch"
	}
	return "ref " + s.Ref
}

// trustLines prints the registry's facts about a rig being pulled (docs/trust.md §6b): numbers, not a score. If the
// registry cannot be asked, it says so.
func trustLines(e env, spec source.Spec, got *source.Fetched) []string {
	owner, name, _ := strings.Cut(spec.Path, "/")
	t, ver, err := regClient(e, spec.URL).TrustFor(context.Background(), owner, name, got.Commit)
	if err != nil {
		return []string{"Trust: the registry could not provide facts about this rig (" + err.Error() + ")"}
	}
	var out []string
	pub := t.Publisher.Login
	if t.Publisher.Verified {
		pub += " [verified " + t.Publisher.VerifiedKind + "]"
	}
	out = append(out, fmt.Sprintf("Trust: %s@%s by %s; %d version(s), %d star(s), first seen %s, %d public rig(s)", spec.Path, ver, pub, t.Versions, t.Stars, shortDate(t.Publisher.FirstSeen), t.Publisher.PublicRigs))
	switch {
	case t.Signature.Signed && t.Signature.ByPublisher:
		out = append(out, "Trust: signed by the publisher's identity ("+t.Signature.Subject+")")
	case t.Signature.Signed:
		out = append(out, "!!! Trust: signed, but NOT by the publisher's identity ("+t.Signature.Subject+")")
	default:
		out = append(out, "Trust: not signed")
	}
	if a := t.Analysis; a["danger"]+a["caution"] > 0 {
		out = append(out, fmt.Sprintf("Trust: the registry's static analysis found %d danger, %d caution pattern(s); see ANALYSIS below", a["danger"], a["caution"]))
	}
	for _, m := range t.SimilarTo {
		out = append(out, fmt.Sprintf("!!! Trust: this name is similar to %s (%s, %d stars%s). Check that you are pulling the rig you mean.", m.Ref, m.Kind, m.Stars, map[bool]string{true: ", verified publisher"}[m.Verified]))
	}
	if t.History.Yanked > 0 || t.History.Removed > 0 {
		out = append(out, fmt.Sprintf("Trust: %d yanked version(s) of this rig; %d version(s) removed by moderators across this publisher's rigs", t.History.Yanked, t.History.Removed))
	}
	return out
}

func shortDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// signatureLines reports the LOCAL verification of the version's signature (the registry's word is not enough).
func signatureLines(got *source.Fetched) []string {
	s := got.Signer
	switch {
	case s == nil:
		return []string{"Signature: none (this version is not signed)"}
	case s.Err != "":
		return []string{"!!! Signature: present but NOT verified here: " + s.Err}
	case s.ByPublisher:
		return []string{"Signature: verified on this machine; signed by the publisher's own GitHub Actions identity: " + s.String()}
	}
	return []string{"!!! Signature: verified on this machine, but the signer is NOT the publisher's identity: " + s.String()}
}

// checkSignature applies --require-signature and refuses an update whose signer changed.
func checkSignature(e env, got *source.Fetched, prev *state.RigRef, require, acceptChange bool) int {
	s := got.Signer
	if require && (s == nil || s.Err != "" || !s.ByPublisher) {
		fmt.Fprintln(e.err, "rigfile: --require-signature: this version is not signed by the publisher's own GitHub Actions identity, or the signature does not verify here.")
		for _, l := range signatureLines(got) {
			fmt.Fprintln(e.err, "  "+l)
		}
		return 1
	}
	if prev != nil && prev.Signer != "" && s.String() != prev.Signer && !acceptChange {
		fmt.Fprintf(e.err, "rigfile: refusing the update: the signer changed.\n  before: %s\n  now:    %s\nA different signer can mean a legitimate change of release process or a takeover. If you have checked, run again with --accept-signer-change.\n", prev.Signer, orNone(s.String()))
		return 1
	}
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "(unsigned)"
	}
	return s
}
