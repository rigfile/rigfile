package githook

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/scan"
)

// ScannerFunc builds the scanner on first use: compiling the rule set costs tens of milliseconds, which the
// reference-transaction backstop must not pay when there is nothing to scan.
type ScannerFunc func() (*scan.Scanner, error)

// Fixed wraps an existing scanner.
func Fixed(sc *scan.Scanner) ScannerFunc { return func() (*scan.Scanner, error) { return sc, nil } }

// DefaultScanner builds the standard scanner (embedded rules, 10 MB limit).
func DefaultScanner() (*scan.Scanner, error) { return scan.New(scan.Options{}) }

// PreCommit is `rigfile hook pre-commit`. It fails CLOSED: if the staged changes cannot be scanned the
// commit is blocked (a human can still use --no-verify; the reference-transaction backstop then applies).
func PreCommit(g Git, scf ScannerFunc, stderr io.Writer) int {
	sc, err := scf()
	if err != nil {
		fmt.Fprintf(stderr, "\n✘ rigfile: commit blocked: scanner unavailable: %v\n", err)
		return 1
	}
	res, err := g.ScanStaged(sc)
	if err != nil {
		fmt.Fprintf(stderr, "\n✘ rigfile: commit blocked: could not scan the staged changes: %v\n", err)
		return 1
	}
	if len(res.Findings) > 0 {
		fmt.Fprint(stderr, Render("commit", res))
		return 1
	}
	for _, n := range res.Notes {
		fmt.Fprintf(stderr, "rigfile: note: %s\n", n)
	}
	if tree, err := g.line("write-tree"); err == nil && tree != "" {
		g.recordScanned(tree)
	}
	return 0
}

// PrePush is `rigfile hook pre-push <remote> <url>`: stdin lines are
// `<local-ref> <local-oid> <remote-ref> <remote-oid>`.
func PrePush(g Git, scf ScannerFunc, remote string, stdin io.Reader, stderr io.Writer) int {
	sc, err := scf()
	if err != nil {
		fmt.Fprintf(stderr, "\n✘ rigfile: push blocked: scanner unavailable: %v\n", err)
		return 1
	}
	var all Result
	sc0 := bufio.NewScanner(stdin)
	for sc0.Scan() {
		f := strings.Fields(sc0.Text())
		if len(f) != 4 {
			continue
		}
		localOID, remoteOID := f[1], f[3]
		if isZero(localOID) { // deleting a ref: nothing to scan
			continue
		}
		commits, base, err := g.commitsToPush(localOID, remoteOID, remote)
		if err != nil {
			fmt.Fprintf(stderr, "\n✘ rigfile: push blocked: could not list the commits being pushed: %v\n", err)
			return 1
		}
		res, err := g.ScanCommits(sc, commits, base)
		if err != nil {
			fmt.Fprintf(stderr, "\n✘ rigfile: push blocked: could not scan the commits being pushed: %v\n", err)
			return 1
		}
		all.Findings = append(all.Findings, res.Findings...)
		all.Notes = append(all.Notes, res.Notes...)
		all.Files += res.Files
	}
	if len(all.Findings) > 0 {
		fmt.Fprint(stderr, Render("push", all))
		return 1
	}
	return 0
}

// commitsToPush lists the commits (oldest first) that the remote does not have yet, and the revision whose
// allow list applies (the remote's current tip, if we have it locally).
func (g Git) commitsToPush(local, remoteOID, remote string) ([]string, string, error) {
	var b []byte
	var err error
	base := ""
	if !isZero(remoteOID) && g.ok("cat-file", "-e", remoteOID+"^{commit}") {
		b, err = g.out("rev-list", "--reverse", remoteOID+".."+local)
		base = remoteOID
	} else {
		args := []string{"rev-list", "--reverse", local, "--not"}
		if remote != "" && g.ok("remote", "get-url", remote) {
			args = append(args, "--remotes="+remote)
		} else {
			args = append(args, "--remotes")
		}
		b, err = g.out(args...)
	}
	if err != nil {
		return nil, "", err
	}
	return strings.Fields(string(b)), base, nil
}

// ReferenceTransaction is `rigfile hook reference-transaction <state>`. Git aborts the transaction when the
// hook exits non-zero in the "prepared" state, and `--no-verify` does not skip it (docs/targets/git.md), so
// this catches commits made with `git commit --no-verify`. Commits whose tree pre-commit already approved
// are skipped, which keeps the common case to a couple of cheap git calls.
//
// It fails OPEN on internal errors (with a loud warning): a bug here must not be able to stop every branch
// switch, rebase and fetch on the machine. Findings still block.
func ReferenceTransaction(g Git, scf ScannerFunc, state string, stdin io.Reader, stderr io.Writer) int {
	if state != "prepared" {
		return 0
	}
	var all Result
	var set map[string]bool
	approved := func() map[string]bool { // loaded lazily: most invocations never need it
		if set == nil {
			set = g.scannedSet()
		}
		return set
	}
	rd := bufio.NewScanner(stdin)
	for rd.Scan() {
		f := strings.Fields(rd.Text())
		if len(f) != 3 {
			continue
		}
		oldOID, newOID, ref := f[0], f[1], f[2]
		if isZero(newOID) || !(strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/tags/")) || oldOID == newOID {
			continue
		}
		var pairs [][2]string
		var err error
		base := ""
		// Fast path, zero subprocesses: the usual `git commit` moves the branch by exactly one new commit
		// whose tree pre-commit already approved.
		if tree, parents, ok := g.looseCommit(newOID); ok && ((isZero(oldOID) && len(parents) == 0) || (len(parents) == 1 && parents[0] == oldOID)) && approved()[tree] {
			continue
		}
		if isZero(oldOID) {
			pairs, err = g.commitTrees(newOID, "--not", "--branches", "--tags", "--remotes")
		} else {
			pairs, err = g.commitTrees(oldOID + ".." + newOID)
			base = oldOID
		}
		if err != nil {
			fmt.Fprintf(stderr, "rigfile: WARNING: reference-transaction check skipped (%v)\n", err)
			return 0
		}
		var todo []string
		for _, p := range pairs {
			if !approved()[p[1]] {
				todo = append(todo, p[0])
			}
		}
		if len(todo) == 0 {
			continue
		}
		sc, err := scf()
		if err != nil {
			fmt.Fprintf(stderr, "rigfile: WARNING: reference-transaction check skipped (%v)\n", err)
			return 0
		}
		res, err := g.ScanCommits(sc, todo, base)
		if err != nil {
			fmt.Fprintf(stderr, "rigfile: WARNING: reference-transaction check skipped (%v)\n", err)
			return 0
		}
		all.Findings = append(all.Findings, res.Findings...)
		all.Notes = append(all.Notes, res.Notes...)
	}
	if len(all.Findings) > 0 {
		fmt.Fprint(stderr, Render("ref update", all))
		return 1
	}
	return 0
}

// CommitMsg is `rigfile hook commit-msg <file>`: the message text is not part of any diff, so a secret pasted
// into a commit message would otherwise be committed unseen. Fails closed like pre-commit.
func CommitMsg(scf ScannerFunc, file string, stderr io.Writer) int {
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(stderr, "\n✘ rigfile: commit blocked: cannot read the commit message: %v\n", err)
		return 1
	}
	sc, err := scf()
	if err != nil {
		fmt.Fprintf(stderr, "\n✘ rigfile: commit blocked: scanner unavailable: %v\n", err)
		return 1
	}
	// comment lines (git strips them) are not part of the message
	var msg strings.Builder
	for _, l := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(l, "#") {
			msg.WriteString(l + "\n")
		}
	}
	fs := sc.ScanText("", msg.String())
	if len(fs) == 0 {
		return 0
	}
	res := Result{Findings: fs, Notes: []string{"the secret is in the commit MESSAGE, which a diff scan would never see; edit the message"}}
	fmt.Fprint(stderr, Render("commit", res))
	return 1
}
