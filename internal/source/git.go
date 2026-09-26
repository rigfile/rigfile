package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GitFetcher fetches any other git URL with the git binary: by exact commit into a fresh empty repository, then
// `git archive` (no checkout, so no hooks or smudge filters run), never following submodules or LFS.
type GitFetcher struct {
	Bin    string   // default "git"
	Env    []string // extra environment (tests)
	Limits Limits
}

func (g *GitFetcher) bin() string {
	if g.Bin != "" {
		return g.Bin
	}
	return "git"
}

func (g *GitFetcher) cmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	base := []string{"-c", "credential.helper=", "-c", "protocol.ext.allow=never", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "submodule.recurse=false", "-c", "core.fsmonitor=false"}
	c := exec.CommandContext(ctx, g.bin(), append(base, args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_ALLOW_PROTOCOL=https:ssh:file", "GIT_LFS_SKIP_SMUDGE=1")
	c.Env = append(c.Env, g.Env...)
	return c
}

func (g *GitFetcher) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := g.cmd(ctx, dir, args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return out.Bytes(), nil
}

// Resolve finds the commit spec.Ref points to (tags win over branches, annotated tags are peeled).
func (g *GitFetcher) Resolve(ctx context.Context, spec Spec) (string, error) {
	if IsCommit(spec.Ref) {
		return spec.Ref, nil
	}
	pats := []string{"HEAD"}
	if spec.Ref != "" {
		pats = []string{"refs/tags/" + spec.Ref + "^{}", "refs/tags/" + spec.Ref, "refs/heads/" + spec.Ref}
	}
	out, err := g.run(ctx, "", append([]string{"ls-remote", "--", spec.URL}, pats...)...)
	if err != nil {
		return "", err
	}
	found := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && IsCommit(f[0]) {
			found[f[1]] = f[0]
		}
	}
	if spec.Ref == "" {
		if sha := found["HEAD"]; sha != "" {
			return sha, nil
		}
	}
	for _, p := range pats {
		if sha := found[p]; sha != "" {
			return sha, nil
		}
	}
	return "", fmt.Errorf("source: %s has no ref %q", spec.URL, spec.Ref)
}

// Fetch downloads exactly `commit` and unpacks its tree into dest.
func (g *GitFetcher) Fetch(ctx context.Context, spec Spec, commit, dest string) error {
	if !IsCommit(commit) {
		return errors.New("source: fetch needs a full commit id")
	}
	work, err := os.MkdirTemp("", "rigfile-git-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if _, err := g.run(ctx, work, "init", "-q"); err != nil {
		return err
	}
	if _, err := g.run(ctx, work, "fetch", "-q", "--depth", "1", "--no-tags", "--no-recurse-submodules", "--", spec.URL, commit); err != nil {
		// a server that will not serve an arbitrary commit: fetch the ref and require it to be that commit
		if spec.Ref == "" || IsCommit(spec.Ref) {
			return err
		}
		if _, err2 := g.run(ctx, work, "fetch", "-q", "--depth", "1", "--no-tags", "--no-recurse-submodules", "--", spec.URL, spec.Ref); err2 != nil {
			return err
		}
		head, err := g.run(ctx, work, "rev-parse", "FETCH_HEAD^{commit}")
		if err != nil || strings.TrimSpace(string(head)) != commit {
			return fmt.Errorf("source: %s moved since it was resolved (expected commit %s)", spec.Ref, commit[:12])
		}
		commit = "FETCH_HEAD"
	} else {
		commit = "FETCH_HEAD"
	}
	c := g.cmd(ctx, work, "archive", "--format=tar", commit)
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	c.Stdout, c.Stderr = pw, &errb
	if err := c.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	pw.Close()
	xerr := Extract(&capReader{r: pr, left: MaxDownload}, dest, false, g.Limits)
	pr.Close()
	werr := c.Wait()
	if xerr != nil {
		return xerr
	}
	if werr != nil {
		return fmt.Errorf("git archive: %w: %s", werr, strings.TrimSpace(errb.String()))
	}
	return nil
}
