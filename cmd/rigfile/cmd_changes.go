package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigfile/rigfile/internal/rigdiff"
	"github.com/rigfile/rigfile/internal/source"
)

// sourceClient is the git/registry source client every pulling command uses.
func sourceClient(e env, sd string) *source.Client {
	if e.sources != nil {
		return e.sources
	}
	return &source.Client{CacheDir: filepath.Join(sd, "sources"), Getenv: e.getenv, Registry: registryFetcher(e)}
}

// cmdChanges implements `changes <before> <after>`: what a new version of a rig adds, removes and changes, with the parts
// that deserve a look listed first. Each side is a rig directory, a registry rig (owner/name@version) or a git source.
func cmdChanges(args []string, e env) int {
	fs := flag.NewFlagSet("changes", flag.ContinueOnError)
	fs.SetOutput(e.err)
	registry := fs.String("registry", "", "Rigfile registry for owner/name@version arguments (default $RIGFILE_REGISTRY)")
	diffs := fs.Bool("diff", false, "also show the text of changed files")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 2 {
		fmt.Fprintln(e.err, "usage: rigfile changes <before> <after> [--diff] [--registry URL]\n  each side: a rig directory, owner/name@version (registry) or a git source")
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
	var dirs [2]string
	for i, arg := range pos {
		side, code := resolveRig(e, client, arg, *registry)
		if code != 0 {
			return code
		}
		dirs[i] = side.dir
	}
	r, err := rigdiff.Rigs(dirs[0], dirs[1], pos[0], pos[1])
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	r.Render(e.out, *diffs)
	return 0
}

// updateChangeLines describes what an update changes, for the review banner. It returns nothing when the previous version
// is no longer in the cache.
func updateChangeLines(prevDir, newDir, from, to string, full bool) []string {
	if prevDir == "" || newDir == "" {
		return nil
	}
	if st, err := os.Stat(prevDir); err != nil || !st.IsDir() {
		return nil
	}
	r, err := rigdiff.Rigs(prevDir, newDir, from, to)
	if err != nil {
		return []string{"Changes: could not compare with the version you have (" + err.Error() + ")"}
	}
	var sb strings.Builder
	r.Render(&sb, full)
	lines := []string{"What changed since the version you have applied:"}
	for _, l := range strings.Split(strings.TrimRight(sb.String(), "\n"), "\n") {
		lines = append(lines, "  "+l)
	}
	return lines
}

// resolvedRig is a rig a command was pointed at: a directory, or something fetched into the source cache.
type resolvedRig struct {
	dir  string
	spec *source.Spec // nil for a directory
}

// resolveRig turns a command-line argument (rig directory, owner/name[@version] in the registry, or a git source) into a
// directory. A non-zero code means it already told the person why.
func resolveRig(e env, client *source.Client, arg, registry string) (resolvedRig, int) {
	switch {
	case isDir(arg):
		return resolvedRig{dir: arg}, 0
	case source.Looks(arg) || registryRef.MatchString(arg):
		var spec source.Spec
		if source.Looks(arg) {
			var err error
			if spec, err = source.Parse(arg); err != nil {
				fmt.Fprintln(e.err, "rigfile:", err)
				return resolvedRig{}, 2
			}
		} else {
			base, err := registryBase(e, registry)
			if err != nil {
				fmt.Fprintln(e.err, "rigfile:", err)
				return resolvedRig{}, 1
			}
			name, ref, _ := strings.Cut(arg, "@")
			spec = source.Spec{Kind: source.Registry, URL: base, Path: name, Ref: ref}
		}
		got, err := client.Get(context.Background(), spec, nil)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return resolvedRig{}, 1
		}
		return resolvedRig{dir: got.Dir, spec: &spec}, 0
	}
	fmt.Fprintf(e.err, "rigfile: %q is not a directory, a registry rig or a git source\n", arg)
	return resolvedRig{}, 2
}
