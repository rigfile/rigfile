package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/digitaldreamer3462/rigfile/internal/source"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// cmdPull implements `pull <source>` and `update`: fetch a rig from a git source into the content-addressed cache,
// then run the normal review-and-apply flow on it, with the source, commit and content hash on the screen.
func cmdPull(verb string, args []string, e env) int {
	var f rigFlags
	fs := rigFlagSet(verb, e, &f, true)
	planOnly := fs.Bool("plan-only", false, "show the plan for the fetched rig and stop (nothing is applied)")
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
	client := e.sources
	if client == nil {
		client = &source.Client{CacheDir: filepath.Join(sd, "sources"), Getenv: e.getenv}
	}

	var spec source.Spec
	var prev *state.RigRef
	switch verb {
	case "pull":
		if len(pos) != 1 {
			fmt.Fprintln(e.err, "usage: rigfile pull <github.com/owner/repo[@ref][//dir] | gitlab.com/... | https://host/repo.git[@ref]> [flags]")
			return 2
		}
		if !source.Looks(pos[0]) {
			fmt.Fprintf(e.err, "rigfile: %q is not a git source. To apply a rig directory on this machine use `rigfile apply %s`.\n", pos[0], pos[0])
			return 2
		}
		if spec, err = source.Parse(pos[0]); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
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

	banner := []string{
		fmt.Sprintf("Source: %s   commit %s   tree %s", spec, got.Commit[:12], got.TreeSHA256[:12]),
		"!!! This rig comes from a git repository you did not write. Review every hook, script and MCP command below:",
		"!!! approving the plan lets them run on this machine. Nothing has run yet.",
	}
	if prev != nil {
		banner = append([]string{fmt.Sprintf("Update: %s -> %s", short12(prev.Commit), short12(got.Commit))}, banner...)
	}
	if spec.Ref == "" || !source.IsCommit(spec.Ref) && prev == nil {
		banner = append(banner, fmt.Sprintf("note: %s was resolved to commit %s now and is pinned to it; `rigfile update` follows the ref later.", refName(spec), got.Commit[:12]))
	}
	f.pulled = &pulledRig{Source: spec.String(), Commit: got.Commit, Tree: got.TreeSHA256, Banner: banner}
	mode := "apply"
	if *planOnly {
		mode = "plan"
	}
	return planApply(mode, got.Dir, f, e)
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
