package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// LocalState is what one device remembers between syncs. It is not secret, but it is what makes rollback and conflict
// detection possible, so it is kept with the device (not in the storage).
type LocalState struct {
	Pin  *Roster           `json:"pin,omitempty"`  // the roster this device trusts
	Base map[string]string `json:"base,omitempty"` // logical name -> plaintext hash last synced
	Seen map[string]int    `json:"seen,omitempty"` // device -> highest index counter this device has seen
}

// Item is one file this device keeps in the vault: its logical name (what the other devices see) and where it lives here.
// The path comes from THIS device's own choice (`track`), never from the storage.
type Item struct {
	Logical string
	Path    string
}

var logicalRe = regexp.MustCompile(`^[A-Za-z0-9._@+-][A-Za-z0-9._@+ -]*(/[A-Za-z0-9._@+-][A-Za-z0-9._@+ -]*)*$`)

// ValidLogical reports whether s is an acceptable logical name (no traversal, no absolute path, no odd characters).
func ValidLogical(s string) bool {
	if len(s) > 180 || !logicalRe.MatchString(s) || strings.Contains(s, "..") {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "." || strings.EqualFold(seg, ".git") {
			return false
		}
	}
	return true
}

// FileWriter writes a local file for a pull (the command wires the journaled writer, so every overwrite is backed up).
type FileWriter interface {
	Write(path string, data []byte, mode os.FileMode) error
}

// Result is the outcome for one file.
type Result struct {
	Logical string
	Action  string // pushed | pulled | unchanged | conflict | refused | missing | skipped
	Detail  string
}

func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func readLocal(path string) ([]byte, os.FileMode, bool, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("%s is not a regular file (links and directories are not synced)", path)
	}
	if fi.Size() > maxFileSize {
		return nil, 0, false, fmt.Errorf("%s is larger than %d MiB", path, maxFileSize>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	return b, fi.Mode().Perm(), true, err
}

// checkFreshness enforces rollback protection and records what was seen.
func (ls *LocalState) checkFreshness(idx *Index) error {
	for dev, c := range ls.Seen {
		if idx.Counters[dev] < c {
			return fmt.Errorf("%w (device %s: the storage says %d, this device has seen %d)", ErrRollback, dev, idx.Counters[dev], c)
		}
	}
	return nil
}

func (ls *LocalState) record(idx *Index) {
	if ls.Seen == nil {
		ls.Seen = map[string]int{}
	}
	for dev, c := range idx.Counters {
		if c > ls.Seen[dev] {
			ls.Seen[dev] = c
		}
	}
}

func (ls *LocalState) setBase(logical, sum string) {
	if ls.Base == nil {
		ls.Base = map[string]string{}
	}
	ls.Base[logical] = sum
}

func (v *Vault) load(ls *LocalState) (*Index, error) {
	idx, err := v.readIndex()
	if err != nil {
		return nil, err
	}
	if err := ls.checkFreshness(idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// Status compares each tracked item with the vault without changing anything, and lists names other devices have that this
// one does not track.
func (v *Vault) Status(items []Item, ls *LocalState) ([]Result, []string, error) {
	idx, err := v.load(ls)
	if err != nil {
		return nil, nil, err
	}
	var out []Result
	tracked := map[string]bool{}
	for _, it := range items {
		tracked[it.Logical] = true
		out = append(out, classify(it, idx, ls))
	}
	var available []string
	for name := range idx.Files {
		if !tracked[name] {
			available = append(available, name)
		}
	}
	sort.Strings(available)
	return out, available, nil
}

func classify(it Item, idx *Index, ls *LocalState) Result {
	local, _, exists, err := readLocal(it.Path)
	if err != nil {
		return Result{it.Logical, "refused", err.Error()}
	}
	rem, inRemote := idx.Files[it.Logical]
	base := ls.Base[it.Logical]
	lsum := ""
	if exists {
		lsum = hashBytes(local)
	}
	switch {
	case !exists && !inRemote:
		return Result{it.Logical, "missing", "not on this device and not in the vault"}
	case !exists:
		return Result{it.Logical, "pull", "in the vault, not on this device"}
	case !inRemote:
		return Result{it.Logical, "push", "not in the vault yet"}
	case lsum == rem.SHA256:
		return Result{it.Logical, "unchanged", "in sync"}
	case lsum == base:
		return Result{it.Logical, "pull", "changed on " + rem.By}
	case rem.SHA256 == base:
		return Result{it.Logical, "push", "changed on this device"}
	}
	return Result{it.Logical, "conflict", "changed on both this device and " + rem.By}
}

// PushOptions gate what may be uploaded.
type PushOptions struct {
	// Scan returns findings (rule names only) for a file's content; a file with findings is refused unless allowed.
	Scan  func(logical string, content []byte) []string
	Allow map[string]bool // logical names the person overrode the scan for
}

// Push uploads the tracked files that changed on this device and that the vault has not changed since. Conflicts are reported,
// not overwritten.
func (v *Vault) Push(items []Item, ls *LocalState, opt PushOptions) ([]Result, error) {
	idx, err := v.load(ls)
	if err != nil {
		return nil, err
	}
	rs, err := v.recipients(v.roster)
	if err != nil {
		return nil, err
	}
	var out []Result
	var stale []string
	changed := false
	for _, it := range items {
		c := classify(it, idx, ls)
		if c.Action != "push" {
			out = append(out, c)
			continue
		}
		data, mode, _, err := readLocal(it.Path)
		if err != nil {
			out = append(out, Result{it.Logical, "refused", err.Error()})
			continue
		}
		if opt.Scan != nil && !opt.Allow[it.Logical] {
			if f := opt.Scan(it.Logical, data); len(f) > 0 {
				out = append(out, Result{it.Logical, "refused", "the secret scanner found " + strings.Join(f, ", ") + ": remove it, or push this file with --allow-secrets " + it.Logical})
				continue
			}
		}
		if len(idx.Files) >= maxFiles {
			out = append(out, Result{it.Logical, "refused", "the vault holds the maximum number of files"})
			continue
		}
		obj, err := v.putObject(data, rs)
		if err != nil {
			return out, err
		}
		if old, ok := idx.Files[it.Logical]; ok && old.Obj != obj {
			stale = append(stale, old.Obj) // removed only once the new index is in place
		}
		idx.Files[it.Logical] = Entry{SHA256: hashBytes(data), Size: int64(len(data)), Mode: uint32(mode), Obj: obj, By: v.Dev.Name, Counter: idx.Counters[v.Dev.Name] + 1}
		ls.setBase(it.Logical, hashBytes(data))
		out = append(out, Result{it.Logical, "pushed", fmt.Sprintf("%d bytes", len(data))})
		changed = true
	}
	if changed {
		if err := v.writeIndex(idx); err != nil {
			return out, err
		}
		for _, o := range stale {
			_ = v.T.Remove(o)
		}
	}
	ls.record(idx)
	return out, nil
}

// Pull applies what other devices changed to the tracked files that this device has not changed. A file changed on both sides is
// left alone and the incoming version is written beside it as <name>.conflict-<device>-<date>.
func (v *Vault) Pull(items []Item, ls *LocalState, wr FileWriter) ([]Result, error) {
	idx, err := v.load(ls)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, it := range items {
		c := classify(it, idx, ls)
		rem, inRemote := idx.Files[it.Logical]
		switch c.Action {
		case "pull":
			data, err := v.getObject(rem)
			if err != nil {
				return out, err
			}
			if err := wr.Write(it.Path, data, os.FileMode(rem.Mode)&0o755|0o600); err != nil {
				return out, err
			}
			ls.setBase(it.Logical, rem.SHA256)
			out = append(out, Result{it.Logical, "pulled", "from " + rem.By})
		case "conflict":
			data, err := v.getObject(rem)
			if err != nil {
				return out, err
			}
			side := fmt.Sprintf("%s.conflict-%s-%s", it.Path, rem.By, v.now().UTC().Format("2006-01-02"))
			if err := wr.Write(side, data, 0o600); err != nil {
				return out, err
			}
			out = append(out, Result{it.Logical, "conflict", "kept yours; the incoming version is at " + side})
		case "unchanged":
			if inRemote {
				ls.setBase(it.Logical, rem.SHA256)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	ls.record(idx)
	return out, nil
}
