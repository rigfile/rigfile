package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

var rigName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}/[a-z0-9][a-z0-9._-]{0,62}$`)

// cmdFork implements `fork <source> --name owner/name`: start your own rig from someone else's. By default it copies the rig
// (a real fork, yours to change); with --extend it writes a small rig that layers on top of the original with `from:`.
func cmdFork(args []string, e env) int {
	fs := flag.NewFlagSet("fork", flag.ContinueOnError)
	fs.SetOutput(e.err)
	name := fs.String("name", "", "the new rig's name, owner/name (required)")
	out := fs.String("out", "", "directory to create (default ./<name>)")
	extend := fs.Bool("extend", false, "write a small rig that builds on the original with `from:` instead of copying it")
	registry := fs.String("registry", "", "Rigfile registry for owner/name arguments (default $RIGFILE_REGISTRY)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 || *name == "" {
		fmt.Fprintln(e.err, "usage: rigfile fork <owner/name[@version] | git source | rig directory> --name <owner/name> [--out dir] [--extend]")
		return 2
	}
	if !rigName.MatchString(*name) {
		fmt.Fprintf(e.err, "rigfile: %q is not a rig name: use owner/name, lower-case letters, digits and hyphens\n", *name)
		return 2
	}
	dest := *out
	if dest == "" {
		dest = (*name)[strings.Index(*name, "/")+1:]
	}
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		fmt.Fprintf(e.err, "rigfile: %s already has files; choose another --out\n", dest)
		return 1
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
	src, code := resolveRig(e, sourceClient(e, sd), pos[0], *registry)
	if code != 0 {
		return code
	}
	orig, err := manifest.Load(src.dir)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}

	created := !isDir(dest)
	fail := func(err error) int {
		fmt.Fprintln(e.err, "rigfile:", err)
		if created {
			_ = os.RemoveAll(dest)
		}
		return 1
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fail(err)
	}
	origin := orig.M.Name + "@" + orig.M.Version
	if *extend {
		base := ""
		switch {
		case src.spec == nil:
			return fail(errors.New("--extend needs a registry or git source to build on, not a local directory (a layer must be something other machines can fetch)"))
		case src.spec.Kind == source.Registry:
			base = orig.M.Name + "@^" + majorMinorOf(orig.M.Version)
		default:
			base = src.spec.String()
		}
		doc := fmt.Sprintf("apiVersion: rigfile.dev/v1\nname: %s\nversion: 0.1.0\ndescription: Builds on %s\nfrom:\n  - %s\n", *name, origin, base)
		if err := os.WriteFile(filepath.Join(dest, manifest.FileName), []byte(doc), 0o644); err != nil {
			return fail(err)
		}
	} else {
		if err := copyRig(src.dir, dest); err != nil {
			return fail(err)
		}
		raw, err := os.ReadFile(filepath.Join(dest, manifest.FileName))
		if err != nil {
			return fail(err)
		}
		doc := rewriteTop(string(raw), "name", *name)
		doc = rewriteTop(doc, "version", "0.1.0")
		doc = "# Forked from " + origin + "\n" + doc
		if err := os.WriteFile(filepath.Join(dest, manifest.FileName), []byte(doc), 0o644); err != nil {
			return fail(err)
		}
	}
	// the result must be a valid rig, or nothing is left behind
	l, err := manifest.Load(dest)
	if err != nil {
		return fail(fmt.Errorf("the new rig does not validate: %w", err))
	}
	if ps := manifest.Check(l); manifest.HasErrors(ps) {
		for _, p := range ps {
			if p.Level == manifest.Error {
				return fail(fmt.Errorf("the new rig does not validate: %s: %s", p.Where, p.Msg))
			}
		}
	}
	readme := filepath.Join(dest, "README.md")
	if _, err := os.Stat(readme); err != nil {
		_ = os.WriteFile(readme, []byte("# "+*name+"\n\nBased on "+origin+".\n"), 0o644)
	}
	fmt.Fprintf(e.out, "Created %s (%s %s).\n", dest, map[bool]string{true: "builds on", false: "forked from"}[*extend], origin)
	fmt.Fprintf(e.out, "Next: edit %s, then `rigfile apply %s` to try it, and `rigfile publish` when it is yours to share.\n", filepath.Join(dest, manifest.FileName), dest)
	return 0
}

func majorMinorOf(v string) string {
	p := strings.SplitN(v, ".", 3)
	if len(p) < 2 {
		return v
	}
	return p[0] + "." + p[1]
}

// rewriteTop replaces a top-level scalar key's value, leaving the rest of the file (comments, order) alone.
func rewriteTop(doc, key, value string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:.*$`)
	if re.MatchString(doc) {
		return re.ReplaceAllLiteralString(doc, key+": "+value)
	}
	return key + ": " + value + "\n" + doc
}

// copyRig copies a rig directory: regular files and directories only, without .git or the lock (which belongs to the
// original). File modes are kept so scripts stay executable.
func copyRig(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		if !d.Type().IsRegular() || rel == "rigfile.lock" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		outf, err := os.OpenFile(filepath.Join(dest, rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(outf, in); err != nil {
			outf.Close()
			return err
		}
		return outf.Close()
	})
}
