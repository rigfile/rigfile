// Package githook implements the git-side checks of base-secure (plan §8.1b-c): `pre-commit` (staged
// changes), `pre-push` (the commits being pushed) and a `reference-transaction` backstop that a
// `--no-verify` cannot skip (docs/targets/git.md). All logic is here, in the Go binary; the files git runs
// are one-line shims.
//
// Threat note. These hooks are a guardrail against accidents and against an agent that tries to commit a
// secret; they are not a control against a determined local human, who can always edit or unset the hook
// configuration. Findings never carry the matched value (internal/scan), and nothing here prints file
// contents. Only ADDED lines are judged, so an old secret sitting untouched in a file does not block an
// unrelated commit (it is `rigfile doctor --git`'s job to find those).
package githook

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Git runs git in Dir ("" = the process's working directory).
type Git struct{ Dir string }

func (g Git) cmd(args ...string) *exec.Cmd {
	c := exec.Command("git", args...)
	c.Dir = g.Dir
	return c
}

// out runs git and returns stdout; a failure carries git's stderr.
func (g Git) out(args ...string) ([]byte, error) {
	c := g.cmd(args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (g Git) line(args ...string) (string, error) {
	b, err := g.out(args...)
	return strings.TrimSpace(string(b)), err
}

// ok reports whether git exits 0 (used for existence probes).
func (g Git) ok(args ...string) bool {
	c := g.cmd(args...)
	return c.Run() == nil
}

// ZeroOID is git's all-zero object name.
const ZeroOID = "0000000000000000000000000000000000000000"

func isZero(oid string) bool { return strings.Trim(oid, "0") == "" }

// Change is one added/copied/modified/renamed file on the new side of a diff.
type Change struct {
	Status byte // A C M R
	Path   string
	OID    string
	Mode   string
}

// parseRaw parses `git diff --raw -z` output.
func parseRaw(b []byte) []Change {
	var out []Change
	toks := bytes.Split(bytes.TrimRight(b, "\x00"), []byte{0})
	for i := 0; i+1 < len(toks); i++ {
		h := string(toks[i])
		if !strings.HasPrefix(h, ":") {
			continue
		}
		f := strings.Fields(h[1:]) // srcmode dstmode srcsha dstsha status
		if len(f) < 5 {
			continue
		}
		out = append(out, Change{Status: f[4][0], Path: string(toks[i+1]), OID: f[3], Mode: f[1]})
		i++
	}
	return out
}

const rawArgs = "--raw"

func (g Git) stagedChanges() ([]Change, error) {
	b, err := g.out("diff", "--cached", rawArgs, "-z", "--no-renames", "--diff-filter=ACMR")
	if err != nil {
		return nil, err
	}
	return parseRaw(b), nil
}

// commitChanges returns the changes of commit c against its first parent (or the empty tree for a root
// commit) and that parent ("" for a root commit).
func (g Git) commitChanges(c string) ([]Change, string, error) {
	parent, perr := g.line("rev-parse", "--verify", "-q", c+"^1")
	var b []byte
	var err error
	if perr != nil || parent == "" {
		parent = ""
		b, err = g.out("diff-tree", "-r", rawArgs, "-z", "--no-renames", "--diff-filter=ACMR", "--no-commit-id", "--root", c)
	} else {
		b, err = g.out("diff-tree", "-r", rawArgs, "-z", "--no-renames", "--diff-filter=ACMR", parent, c)
	}
	if err != nil {
		return nil, "", err
	}
	return parseRaw(b), parent, nil
}

// addedRanges returns the [start,end] line ranges added to path. all is true when the whole file is new.
func (g Git) addedRanges(args []string, path string, status byte) (ranges [][2]int, all bool, err error) {
	if status == 'A' {
		return nil, true, nil
	}
	b, err := g.out(append(append([]string{"diff", "-U0", "--no-renames", "--no-color"}, args...), "--", path)...)
	if err != nil {
		return nil, false, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(l, "@@ ") {
			continue
		}
		i := strings.Index(l, " +")
		if i < 0 {
			continue
		}
		spec := l[i+2:]
		if j := strings.Index(spec, " "); j >= 0 {
			spec = spec[:j]
		}
		start, count := spec, "1"
		if k := strings.Index(spec, ","); k >= 0 {
			start, count = spec[:k], spec[k+1:]
		}
		s, e1 := strconv.Atoi(start)
		n, e2 := strconv.Atoi(count)
		if e1 != nil || e2 != nil {
			return nil, false, fmt.Errorf("githook: cannot parse hunk header %q", l)
		}
		if n > 0 {
			ranges = append(ranges, [2]int{s, s + n - 1})
		}
	}
	return ranges, false, nil
}

// catBatch reads blobs through one `git cat-file --batch` process.
type catBatch struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func (g Git) newCatBatch() (*catBatch, error) {
	c := g.cmd("cat-file", "--batch")
	in, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return &catBatch{cmd: c, in: in, out: bufio.NewReaderSize(out, 1<<16)}, nil
}

// read returns the blob's size and, when size <= max, its content.
func (c *catBatch) read(oid string, max int) (data []byte, size int, err error) {
	if _, err := io.WriteString(c.in, oid+"\n"); err != nil {
		return nil, 0, err
	}
	hdr, err := c.out.ReadString('\n')
	if err != nil {
		return nil, 0, err
	}
	f := strings.Fields(hdr)
	if len(f) == 2 && f[1] == "missing" {
		return nil, 0, fmt.Errorf("githook: object %s is missing", oid)
	}
	if len(f) != 3 {
		return nil, 0, fmt.Errorf("githook: unexpected cat-file header %q", strings.TrimSpace(hdr))
	}
	size, err = strconv.Atoi(f[2])
	if err != nil {
		return nil, 0, err
	}
	if size > max {
		_, err = io.CopyN(io.Discard, c.out, int64(size)+1)
		return nil, size, err
	}
	data = make([]byte, size)
	if _, err = io.ReadFull(c.out, data); err != nil {
		return nil, 0, err
	}
	_, err = c.out.ReadByte() // trailing LF
	return data, size, err
}

// lookup reads any object spec (`HEAD:.rigfile-allow`, `:path`); found is false when it does not exist.
func (c *catBatch) lookup(spec string, max int) (data []byte, found bool, err error) {
	if _, err := io.WriteString(c.in, spec+"\n"); err != nil {
		return nil, false, err
	}
	hdr, err := c.out.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	f := strings.Fields(hdr)
	if len(f) >= 2 && (f[len(f)-1] == "missing" || f[len(f)-1] == "ambiguous") {
		return nil, false, nil
	}
	if len(f) != 3 {
		return nil, false, fmt.Errorf("githook: unexpected cat-file header %q", strings.TrimSpace(hdr))
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, false, err
	}
	if size > max {
		_, err = io.CopyN(io.Discard, c.out, int64(size)+1)
		return nil, false, err
	}
	data = make([]byte, size)
	if _, err = io.ReadFull(c.out, data); err != nil {
		return nil, false, err
	}
	_, err = c.out.ReadByte()
	return data, true, err
}

func (c *catBatch) close() {
	_ = c.in.Close()
	_ = c.cmd.Wait()
}

var errNoRepo = errors.New("not inside a git repository")
