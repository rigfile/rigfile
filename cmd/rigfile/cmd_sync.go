package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/apply"
	"github.com/digitaldreamer3462/rigfile/internal/manifest"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
	"github.com/digitaldreamer3462/rigfile/internal/vault"
)

const syncUsage = `usage: rigfile sync <command>

  init <dir> [--name device] [--git]       start an encrypted vault in <dir> (a private git repo or a synced folder)
  join <dir> [--name device] [--git]       ask an enrolled device to let this one in (prints a fingerprint)
  approve <device> --fingerprint FP        enrol a device that asked to join (FP is what ITS screen shows)
  finish --fingerprint VFP                 on the joining device: trust the vault (VFP is what the approving device shows)
  devices | revoke <device> | rekey        list, remove (re-encrypts everything), or repair after an interrupted change
  track <path> [--rig <dir>]               keep a file in the vault (under your home, or a private: path of a rig)
  untrack <path|name>
  status                                   what would be pushed or pulled
  push [--allow-secrets NAME]... | pull    send your changes / receive theirs (backups first; conflicts are kept apart)
`

const deviceRef = "sync/device"

type syncConfig struct {
	Dir     string            `json:"dir"`
	Device  string            `json:"device"`
	Git     bool              `json:"git,omitempty"`
	Tracked map[string]string `json:"tracked,omitempty"` // logical name -> path on THIS device
}

type syncCtx struct {
	e     env
	sd    string
	home  string
	cfg   syncConfig
	ls    vault.LocalState
	store secrets.Store
	dev   *vault.Device
	v     *vault.Vault
	tr    vault.DirTransport
}

func syncDir(sd string) string { return filepath.Join(sd, "sync") }

func (c *syncCtx) save() error {
	b, _ := json.MarshalIndent(c.cfg, "", "  ")
	if err := platform.WritePrivate(filepath.Join(syncDir(c.sd), "config.json"), append(b, '\n')); err != nil {
		return err
	}
	b, _ = json.MarshalIndent(c.ls, "", "  ")
	return platform.WritePrivate(filepath.Join(syncDir(c.sd), "state.json"), append(b, '\n'))
}

func loadSync(e env, needVault bool) (*syncCtx, int) {
	fail := func(err error) (*syncCtx, int) { fmt.Fprintln(e.err, "rigfile:", err); return nil, 1 }
	pi, err := platformInfo(e)
	if err != nil {
		return fail(err)
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		return fail(err)
	}
	home, err := pi.Home()
	if err != nil {
		return fail(err)
	}
	c := &syncCtx{e: e, sd: sd, home: home}
	if b, err := os.ReadFile(filepath.Join(syncDir(sd), "config.json")); err == nil {
		if json.Unmarshal(b, &c.cfg) != nil {
			return fail(errors.New("the sync settings are damaged"))
		}
	} else if needVault {
		return fail(errors.New("this device is not in a vault: run `rigfile sync init <dir>` or `rigfile sync join <dir>`"))
	}
	if b, err := os.ReadFile(filepath.Join(syncDir(sd), "state.json")); err == nil {
		_ = json.Unmarshal(b, &c.ls)
	}
	if needVault {
		if c.store, err = openStore(e, pi); err != nil {
			return fail(err)
		}
		raw, err := c.store.Get(deviceRef)
		if err != nil {
			return fail(fmt.Errorf("this device's vault identity is not in the secret store: %w", err))
		}
		if c.dev, err = vault.ParseDevice(raw); err != nil {
			return fail(err)
		}
		c.tr = vault.DirTransport{Root: c.cfg.Dir}
		if c.cfg.Git {
			if err := gitPull(c.cfg.Dir); err != nil {
				return fail(err)
			}
		}
		if c.v, err = vault.Open(c.tr, c.dev, c.ls.Pin, nil); err != nil {
			return fail(err)
		}
		c.ls.Pin = ptrRoster(c.v.Roster())
	}
	return c, 0
}

func ptrRoster(r vault.Roster) *vault.Roster { return &r }

// ---- git transport: the vault directory is a repository you own; Rigfile only fast-forwards and commits, never force-pushes ----

func git(dir string, args ...string) error {
	full := append([]string{"-C", dir, "-c", "user.name=rigfile", "-c", "user.email=rigfile@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func hasRemote(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "remote").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func gitPull(dir string) error {
	if !hasRemote(dir) {
		return nil
	}
	// only a branch that already tracks something can be fast-forwarded (a fresh repository has nothing to pull yet)
	if exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Run() != nil {
		return nil
	}
	return git(dir, "pull", "--ff-only", "--quiet")
}

func gitPush(dir, msg string) error {
	if err := git(dir, "add", "-A"); err != nil {
		return err
	}
	if exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() == nil {
		return nil // nothing changed
	}
	if err := git(dir, "commit", "--quiet", "-m", msg); err != nil {
		return err
	}
	if hasRemote(dir) {
		return git(dir, "push", "--quiet", "-u", "origin", "HEAD")
	}
	return nil
}

// ---- tracking ----------------------------------------------------------------------------------

var neverSync = regexp.MustCompile(`(?i)(^|/)(\.ssh|\.aws|\.gnupg|\.rigfile|\.kube|\.docker|\.netrc|\.npmrc|\.pypirc|\.env(\..*)?|id_[a-z0-9]+|credentials(\..*)?|\.git-credentials)(/|$)`)

// logicalFor decides the name other devices see, from a path. Only files under the home directory, or a private: path of a rig
// the person points at, can be tracked; well-known credential locations never can.
func (c *syncCtx) logicalFor(path, rigDir string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if rigDir != "" {
		l, err := manifest.Load(rigDir)
		if err != nil {
			return "", err
		}
		rd, _ := filepath.Abs(l.Dir)
		rel, err := filepath.Rel(rd, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("%s is not inside the rig %s", path, rigDir)
		}
		rel = filepath.ToSlash(rel)
		ok := false
		for _, p := range l.M.Private {
			p = strings.TrimSuffix(filepath.ToSlash(p), "/")
			if rel == p || strings.HasPrefix(rel, p+"/") {
				ok = true
			}
		}
		if !ok {
			return "", fmt.Errorf("%s is not listed under `private:` in the rig's rigfile.yaml, so it is not something the rig says is yours to sync", rel)
		}
		return "rig/" + l.M.Name + "/" + rel, nil
	}
	rel, err := filepath.Rel(c.home, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside your home directory; only files under it (or a rig's private: paths, with --rig) can be tracked", path)
	}
	return "home/" + filepath.ToSlash(rel), nil
}

func (c *syncCtx) items() []vault.Item {
	names := make([]string, 0, len(c.cfg.Tracked))
	for n := range c.cfg.Tracked {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]vault.Item, 0, len(names))
	for _, n := range names {
		out = append(out, vault.Item{Logical: n, Path: c.cfg.Tracked[n]})
	}
	return out
}

func defaultDeviceName() string {
	h, _ := os.Hostname()
	h = strings.ToLower(strings.Split(h, ".")[0])
	h = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(h, "-")
	h = strings.Trim(h, "-")
	if len(h) > 30 {
		h = h[:30]
	}
	if !vault.ValidName(h) {
		return "device"
	}
	return h
}

type journalWriter struct{ w *apply.Writer }

func (j journalWriter) Write(p string, b []byte, m os.FileMode) error {
	_, err := j.w.WriteFileMode(p, b, m)
	return err
}

func cmdSync(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, syncUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet("sync "+verb, flag.ContinueOnError)
	fs.SetOutput(e.err)
	name := fs.String("name", "", "this device's name")
	useGit := fs.Bool("git", false, "the vault directory is a git repository: fast-forward before, commit and push after")
	fp := fs.String("fingerprint", "", "the fingerprint shown on the other device")
	rig := fs.String("rig", "", "track: the rig directory whose private: list allows this path")
	var allow kvFlags
	fs.Var(&allow, "allow-secrets", "push: send this file even though the scanner found something (repeatable, by logical name)")
	pos, err := parseInterspersed(fs, rest)
	if err != nil {
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(e.err, "rigfile:", err); return 1 }
	usage := func() int { fmt.Fprint(e.err, syncUsage); return 2 }

	switch verb {
	case "init", "join":
		if len(pos) != 1 {
			return usage()
		}
		c, code := loadSync(e, false)
		if c == nil {
			return code
		}
		if c.cfg.Dir != "" {
			return fail(fmt.Errorf("this device is already in a vault (%s)", c.cfg.Dir))
		}
		dir, err := filepath.Abs(pos[0])
		if err != nil {
			return fail(err)
		}
		dn := *name
		if dn == "" {
			dn = defaultDeviceName()
		}
		dev, err := vault.NewDevice(dn)
		if err != nil {
			return fail(err)
		}
		pi, _ := platformInfo(e)
		if c.store, err = openStore(e, pi); err != nil {
			return fail(err)
		}
		tr := vault.DirTransport{Root: dir}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail(err)
		}
		if *useGit {
			if err := gitPull(dir); err != nil {
				return fail(err)
			}
		}
		info, err := dev.Info(timeNow())
		if err != nil {
			return fail(err)
		}
		c.cfg = syncConfig{Dir: dir, Device: dn, Git: *useGit, Tracked: map[string]string{}}
		if verb == "init" {
			v, err := vault.Create(tr, dev, timeNow())
			if err != nil {
				return fail(err)
			}
			c.ls.Pin = ptrRoster(v.Roster())
			fmt.Fprintf(e.out, "Created a vault in %s for device %q.\nVault fingerprint: %s   (compare it when another device joins)\n", dir, dn, v.Roster().Fingerprint())
		} else {
			if _, err := vault.RequestJoin(tr, dev, timeNow()); err != nil {
				return fail(err)
			}
			fmt.Fprintf(e.out, "Asked to join the vault in %s as %q.\nThis device's fingerprint: %s\nOn an enrolled device run:\n  rigfile sync approve %s --fingerprint %s\nthen here: rigfile sync finish --fingerprint <the vault fingerprint it prints>\n", dir, dn, info.Fingerprint(), dn, info.Fingerprint())
		}
		if err := c.store.Set(deviceRef, dev.Marshal()); err != nil {
			return fail(err)
		}
		if err := c.save(); err != nil {
			return fail(err)
		}
		if *useGit {
			_ = gitPush(dir, "rigfile sync: "+verb+" "+dn)
		}
		return 0

	case "finish":
		c, code := loadSyncPending(e)
		if c == nil {
			return code
		}
		if *useGit || c.cfg.Git {
			if err := gitPull(c.cfg.Dir); err != nil {
				return fail(err)
			}
		}
		if *fp == "" {
			return fail(errors.New("--fingerprint is required: the vault fingerprint the approving device printed"))
		}
		r, err := vault.FinishJoin(c.tr, c.dev, *fp)
		if err != nil {
			return fail(err)
		}
		c.ls.Pin = r
		if err := c.save(); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "This device is now in the vault (%d device(s)). Track files with `rigfile sync track`.\n", len(r.Devices))
		return 0
	}

	c, code := loadSync(e, true)
	if c == nil {
		return code
	}
	commit := func(msg string) {
		if c.cfg.Git {
			if err := gitPush(c.cfg.Dir, msg); err != nil {
				fmt.Fprintln(e.err, "rigfile: the vault changed but pushing it failed:", err)
			}
		}
	}
	switch verb {
	case "devices":
		for _, d := range c.v.Roster().Devices {
			mark := ""
			if d.Name == c.dev.Name {
				mark = "  (this device)"
			}
			fmt.Fprintf(e.out, "%s  %s%s\n", d.Name, d.Fingerprint(), mark)
		}
		if pend, _ := vault.Pending(c.tr); len(pend) > 0 {
			for _, p := range pend {
				fmt.Fprintf(e.out, "%s  %s  (asked to join: approve only if this fingerprint is on its own screen)\n", p.Name, p.Fingerprint())
			}
		}
		fmt.Fprintf(e.out, "vault fingerprint: %s\n", c.v.Roster().Fingerprint())
		return 0
	case "approve":
		if len(pos) != 1 || *fp == "" {
			return usage()
		}
		if err := c.v.Approve(pos[0], *fp); err != nil {
			return fail(err)
		}
		c.ls.Pin = ptrRoster(c.v.Roster())
		if err := c.save(); err != nil {
			return fail(err)
		}
		commit("rigfile sync: approve " + pos[0])
		fmt.Fprintf(e.out, "Enrolled %s. Everything was re-encrypted to include it.\nOn %s run: rigfile sync finish --fingerprint %s\n", pos[0], pos[0], c.v.Roster().Fingerprint())
		return 0
	case "revoke":
		if len(pos) != 1 {
			return usage()
		}
		if err := c.v.Revoke(pos[0]); err != nil {
			return fail(err)
		}
		c.ls.Pin = ptrRoster(c.v.Roster())
		if err := c.save(); err != nil {
			return fail(err)
		}
		commit("rigfile sync: revoke " + pos[0])
		fmt.Fprintf(e.out, "Removed %s and re-encrypted everything to the remaining devices.\nIt keeps whatever it had already read (and older copies of the vault, such as git history): treat those contents as exposed and rotate anything sensitive in them.\n", pos[0])
		return 0
	case "rekey":
		if err := c.v.Rekey(); err != nil {
			return fail(err)
		}
		commit("rigfile sync: rekey")
		fmt.Fprintln(e.out, "Re-encrypted the vault to the current devices.")
		return 0
	case "track":
		if len(pos) != 1 {
			return usage()
		}
		abs, err := filepath.Abs(pos[0])
		if err != nil {
			return fail(err)
		}
		if neverSync.MatchString(filepath.ToSlash(abs)) {
			return fail(fmt.Errorf("%s looks like a credential or Rigfile's own state: it is never synced", pos[0]))
		}
		logical, err := c.logicalFor(abs, *rig)
		if err != nil {
			return fail(err)
		}
		if !vault.ValidLogical(logical) {
			return fail(fmt.Errorf("the name %q cannot be used in the vault (odd characters)", logical))
		}
		if c.cfg.Tracked == nil {
			c.cfg.Tracked = map[string]string{}
		}
		c.cfg.Tracked[logical] = abs
		if err := c.save(); err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Tracking %s as %s\n", abs, logical)
		return 0
	case "untrack":
		if len(pos) != 1 {
			return usage()
		}
		for n, p := range c.cfg.Tracked {
			if n == pos[0] {
				delete(c.cfg.Tracked, n)
			} else if abs, _ := filepath.Abs(pos[0]); abs == p {
				delete(c.cfg.Tracked, n)
			}
		}
		if err := c.save(); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "No longer tracked on this device (the vault keeps its copy).")
		return 0
	case "status":
		res, avail, err := c.v.Status(c.items(), &c.ls)
		if err != nil {
			return fail(err)
		}
		if len(res) == 0 {
			fmt.Fprintln(e.out, "nothing is tracked on this device (rigfile sync track <path>)")
		}
		for _, r := range res {
			fmt.Fprintf(e.out, "%-10s %s   %s\n", r.Action, r.Logical, r.Detail)
		}
		for _, n := range avail {
			fmt.Fprintf(e.out, "available  %s   (in the vault; `rigfile sync track` a path for it to receive it)\n", n)
		}
		return 0
	case "push":
		sc, err := scan.New(scan.Options{})
		if err != nil {
			return fail(err)
		}
		allowed := map[string]bool{}
		for _, a := range allow {
			allowed[a] = true
		}
		res, err := c.v.Push(c.items(), &c.ls, vault.PushOptions{Allow: allowed, Scan: func(logical string, b []byte) []string {
			var ids []string
			for _, f := range sc.ScanFile(logical, b) {
				ids = append(ids, f.RuleID)
			}
			return ids
		}})
		if err != nil {
			return fail(err)
		}
		if err := c.save(); err != nil {
			return fail(err)
		}
		printSyncResults(e, res)
		commit("rigfile sync: push from " + c.dev.Name)
		return exitFor(res)
	case "pull":
		w := &apply.Writer{BackupRoot: filepath.Join(c.sd, "backups")}
		res, err := c.v.Pull(c.items(), &c.ls, journalWriter{w})
		if err != nil {
			return fail(err)
		}
		if id, cerr := w.Commit("sync pull"); cerr != nil {
			return fail(cerr)
		} else if id != "" {
			fmt.Fprintf(e.out, "(undo with: rigfile rollback %s)\n", id)
		}
		if err := c.save(); err != nil {
			return fail(err)
		}
		printSyncResults(e, res)
		return exitFor(res)
	}
	return usage()
}

func printSyncResults(e env, res []vault.Result) {
	for _, r := range res {
		fmt.Fprintf(e.out, "%-10s %s   %s\n", r.Action, r.Logical, r.Detail)
	}
	if len(res) == 0 {
		fmt.Fprintln(e.out, "nothing tracked")
	}
}

func exitFor(res []vault.Result) int {
	for _, r := range res {
		if r.Action == "conflict" || r.Action == "refused" {
			return 3
		}
	}
	return 0
}

// loadSyncPending is loadSync for a device that has asked to join but is not yet pinned to a roster.
func loadSyncPending(e env) (*syncCtx, int) {
	c, code := loadSync(e, false)
	if c == nil {
		return nil, code
	}
	if c.cfg.Dir == "" {
		fmt.Fprintln(e.err, "rigfile: this device has not asked to join a vault (rigfile sync join <dir>)")
		return nil, 1
	}
	pi, _ := platformInfo(e)
	var err error
	if c.store, err = openStore(e, pi); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return nil, 1
	}
	raw, err := c.store.Get(deviceRef)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile: this device's vault identity is not in the secret store:", err)
		return nil, 1
	}
	if c.dev, err = vault.ParseDevice(raw); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return nil, 1
	}
	c.tr = vault.DirTransport{Root: c.cfg.Dir}
	return c, 0
}

func timeNow() time.Time { return time.Now() }
