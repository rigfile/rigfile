// Package state records what Rigfile applied (state.json) and checks the machine for drift against it.
//
// Ownership model (docs/merge-semantics.md §8): Rigfile only ever modifies things it can name in this
// file. `diff` and `doctor` compare the recorded hashes with what is on disk; `update` and `rollback`
// act only on recorded items. State holds no secrets, only names, paths and hashes.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/jsonedit"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/splice"
)

// FileName is state.json inside the state directory.
const FileName = "state.json"

// Version of the state format.
const Version = 1

// Item kinds.
const (
	KindFile     = "file"      // a whole file Rigfile wrote; Hash = sha256 of content
	KindTree     = "tree"      // a directory (a skill); Hash = hashing.Tree
	KindRegion   = "region"    // a marker-delimited region inside a user file; Hash = marker hash; Detail: region, style
	KindJSONList = "json-list" // one string inside a JSON array; Detail: list ("permissions.deny"), value
	KindJSONRaw  = "json-raw"  // one structured entry in a JSON array (a hook definition); Detail: list, raw (compacted JSON)
	KindMCP      = "mcp"       // an MCP server registered through the vendor CLI; Detail: name; checked by a probe
)

// State is the whole file.
type State struct {
	Version int                     `json:"version"`
	Targets map[string]*TargetState `json:"targets"`
}

// RigRef identifies what was applied.
type RigRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Hash    string `json:"hash"` // hash of the merged model for this target
}

// TargetState is what was applied for one target (e.g. "claude-code").
type TargetState struct {
	Rig       RigRef `json:"rig"`
	LockSHA   string `json:"lockSha256,omitempty"`
	AppliedAt string `json:"appliedAt,omitempty"`
	RunID     string `json:"runId,omitempty"`
	Items     []Item `json:"items"`
	Needs     []Need `json:"needs,omitempty"`
}

// Need is something the applied rig requires from the user (names only; never a value).
type Need struct {
	Kind        string `json:"kind"` // "secret" | "login"
	Ref         string `json:"ref"`  // secret ref path, or login provider
	Description string `json:"description,omitempty"`
	ObtainURL   string `json:"obtainUrl,omitempty"`
	Method      string `json:"method,omitempty"`
}

// Item is one thing Rigfile owns.
type Item struct {
	Category string            `json:"category"`
	Key      string            `json:"key"`
	Kind     string            `json:"kind"`
	Path     string            `json:"path,omitempty"`
	Hash     string            `json:"hash,omitempty"`
	Detail   map[string]string `json:"detail,omitempty"`
}

// New returns an empty state.
func New() *State { return &State{Version: Version, Targets: map[string]*TargetState{}} }

// Load reads state.json from dir. A missing file is an empty state, not an error.
func Load(dir string) (*State, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("state.json is corrupted: %w (move it aside to start fresh; Rigfile will no longer know what it applied)", err)
	}
	if s.Version != Version {
		return nil, fmt.Errorf("state.json has version %d; this rigfile understands %d", s.Version, Version)
	}
	if s.Targets == nil {
		s.Targets = map[string]*TargetState{}
	}
	return &s, nil
}

// Marshal renders state.json (items sorted for stable output). Callers write it through the
// journaled apply.Writer so `rollback` restores it.
func (s *State) Marshal() ([]byte, error) {
	for _, t := range s.Targets {
		sort.SliceStable(t.Items, func(i, j int) bool {
			a, b := t.Items[i], t.Items[j]
			if a.Category != b.Category {
				return a.Category < b.Category
			}
			if a.Key != b.Key {
				return a.Key < b.Key
			}
			return Identity(a) < Identity(b)
		})
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Save writes state.json atomically with private permissions (direct write, not journaled; the CLI
// uses Marshal + the journaled writer instead).
func (s *State) Save(dir string) error {
	b, err := s.Marshal()
	if err != nil {
		return err
	}
	return platform.WritePrivate(filepath.Join(dir, FileName), b)
}

// Target returns (creating if needed) the state for a target.
func (s *State) Target(name string) *TargetState {
	t := s.Targets[name]
	if t == nil {
		t = &TargetState{}
		s.Targets[name] = t
	}
	return t
}

// Upsert adds or replaces an item, identified by category+key+path.
func (t *TargetState) Upsert(it Item) {
	for i, e := range t.Items {
		if Identity(e) == Identity(it) {
			t.Items[i] = it
			return
		}
	}
	t.Items = append(t.Items, it)
}

// Remove deletes items matching category and key (all paths).
func (t *TargetState) Remove(category, key string) {
	out := t.Items[:0]
	for _, e := range t.Items {
		if !(e.Category == category && e.Key == key) {
			out = append(out, e)
		}
	}
	t.Items = out
}

// Status of one drift check.
type Status string

const (
	OK       Status = "ok"
	Missing  Status = "missing"  // Rigfile wrote it and it is gone
	Modified Status = "modified" // present but differs from what Rigfile wrote
	Unknown  Status = "unknown"  // could not be checked (see Detail)
)

// Drift is the result of checking one item.
type Drift struct {
	Item   Item
	Status Status
	Detail string
}

// Probe checks an item kind that cannot be verified from files alone (KindMCP).
type Probe func(Item) (Status, string)

// Check compares items with the machine. probes supplies checkers for kinds like KindMCP; an item of a
// kind with no probe is reported Unknown, never silently OK.
func Check(items []Item, probes map[string]Probe) []Drift {
	out := make([]Drift, 0, len(items))
	for _, it := range items {
		st, detail := checkOne(it, probes)
		out = append(out, Drift{it, st, detail})
	}
	return out
}

// Clean reports whether every drift is OK.
func Clean(ds []Drift) bool {
	for _, d := range ds {
		if d.Status != OK {
			return false
		}
	}
	return true
}

func checkOne(it Item, probes map[string]Probe) (Status, string) {
	switch it.Kind {
	case KindFile:
		sum, err := hashing.File(it.Path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Missing, ""
		case err != nil:
			return Unknown, err.Error()
		case sum != it.Hash:
			return Modified, "content differs from what Rigfile wrote"
		}
		return OK, ""
	case KindTree:
		sum, err := hashing.Tree(it.Path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Missing, ""
		case err != nil:
			return Unknown, err.Error()
		case sum != it.Hash:
			return Modified, "files in the directory differ from what Rigfile wrote"
		}
		return OK, ""
	case KindRegion:
		doc, err := os.ReadFile(it.Path)
		if errors.Is(err, os.ErrNotExist) {
			return Missing, "file is gone"
		}
		if err != nil {
			return Unknown, err.Error()
		}
		style := splice.Hash
		if it.Detail["style"] == "html" {
			style = splice.HTML
		}
		r, found, err := splice.Find(doc, style, it.Detail["region"])
		switch {
		case err != nil:
			return Unknown, err.Error()
		case !found:
			return Missing, "the managed section was removed"
		case r.Drifted:
			return Modified, "the managed section was edited by hand"
		case it.Hash != "" && r.Hash != it.Hash:
			return Modified, "the managed section was replaced"
		}
		return OK, ""
	case KindJSONList:
		doc, err := os.ReadFile(it.Path)
		if errors.Is(err, os.ErrNotExist) {
			return Missing, "file is gone"
		}
		if err != nil {
			return Unknown, err.Error()
		}
		vals, err := jsonedit.ReadStrings(doc, splitPath(it.Detail["list"]))
		if err != nil {
			return Unknown, err.Error()
		}
		for _, v := range vals {
			if v == it.Detail["value"] {
				return OK, ""
			}
		}
		return Missing, "the entry was removed"
	}
	if it.Kind == KindJSONRaw {
		doc, err := os.ReadFile(it.Path)
		if errors.Is(err, os.ErrNotExist) {
			return Missing, "file is gone"
		}
		if err != nil {
			return Unknown, err.Error()
		}
		vals, err := jsonedit.ReadRaw(doc, splitPath(it.Detail["list"]))
		if err != nil {
			return Unknown, err.Error()
		}
		for _, v := range vals {
			if v == it.Detail["raw"] {
				return OK, ""
			}
		}
		return Missing, "the entry was removed or edited"
	}
	if p, ok := probes[it.Kind]; ok {
		return p(it)
	}
	return Unknown, "no checker for kind " + it.Kind
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for _, r := range p {
		if r == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

// Identity is a stable key for an item: two records with the same identity describe the same owned thing.
func Identity(it Item) string {
	return it.Category + "|" + it.Key + "|" + it.Kind + "|" + it.Path + "|" + it.Detail["list"] + "|" + it.Detail["value"] + "|" + it.Detail["raw"] + "|" + it.Detail["region"] + "|" + it.Detail["name"]
}
