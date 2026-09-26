package claudecode

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/digitaldreamer3462/rigfile/internal/engine"
	"github.com/digitaldreamer3462/rigfile/internal/hashing"
	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// StateTarget is this adapter's key in state.json.
const StateTarget = "claude-code"

// MCPClient is the seam to the vendor CLI (ADR 0002). Tests use an in-memory fake; production uses
// CLIClient, which shells out to `claude mcp ...`.
type MCPClient interface {
	Available() bool
	Present(name string) (bool, error)
	AddJSON(name, jsonDoc string) error
	Remove(name string) error
}

// Env is everything the adapter needs from the machine. Nothing here reads the environment itself:
// tests pass a temp home, real runs pass the real one.
type Env struct {
	Plat       *platform.Info
	ClaudeDir  string             // ~/.claude (or $CLAUDE_CONFIG_DIR)
	ProjectDir string             // "" = project-scope items are skipped with a note
	State      *state.TargetState // what a previous apply recorded (nil = nothing)
	MCP        MCPClient          // nil = the claude CLI is unavailable
	RigfileCmd string             // command used in MCP/hook entries; default "rigfile"
	Overwrite  bool               // replace hand-edited managed content and files Rigfile does not own
	BaseSecure bool               // rigfile/base-secure is part of this run: add its non-rule settings (S2-M5)
	Sandbox    bool               // opt-in: also turn on Claude Code's OS-level sandbox with base-secure's credential denies (S2-M5b)
	Have       func(string) bool  // is this command on PATH? nil = exec.LookPath
}

func (e Env) rigfile() string {
	if e.RigfileCmd == "" {
		return "rigfile"
	}
	return e.RigfileCmd
}

func (e Env) short(p string) string {
	h, _ := e.Plat.Home()
	return engine.Short(p, h)
}

func (e Env) join(elem ...string) string { return filepath.Join(elem...) }

// owned returns the recorded item for category/key/path.
func (e Env) owned(category, key, path string) (state.Item, bool) {
	if e.State == nil {
		return state.Item{}, false
	}
	for _, it := range e.State.Items {
		if it.Category == category && it.Key == key && it.Path == path {
			return it, true
		}
	}
	return state.Item{}, false
}

// ClaudeDirFor resolves ~/.claude, honouring CLAUDE_CONFIG_DIR (docs/targets/claude-code.md §2).
func ClaudeDirFor(pi *platform.Info, getenv func(string) string) (string, error) {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	h, err := pi.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude"), nil
}

func readOptional(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// fileOp plans writing one whole file that Rigfile owns (agents, commands, hook scripts).
func (b *builder) fileOp(category, key, dest string, want []byte, mode os.FileMode, from string, runs bool) {
	env := b.env
	wantHash := hashing.Bytes(want)
	item := state.Item{Category: category, Key: key, Kind: state.KindFile, Path: dest, Hash: wantHash}
	label := fmt.Sprintf("%s → %s", key, env.short(dest))
	cur, exists, err := readOptional(dest)
	if err != nil {
		b.fail(fmt.Errorf("%s %s: %w", category, key, err))
		return
	}
	op := engine.Op{Category: category, Key: key, Items: []state.Item{item}, Runs: runs}
	write := func(x *engine.Exec) error {
		_, err := x.W.WriteFileMode(dest, want, mode)
		return err
	}
	switch {
	case !exists:
		op.Symbol, op.Summary, op.Do = engine.New, label, write
	case bytes.Equal(cur, want):
		op.Symbol, op.Summary = engine.Unchanged, label+"   (up to date)"
	default:
		prev, mine := env.owned(category, key, dest)
		switch {
		case mine && prev.Hash == hashing.Bytes(cur):
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (updated from "+from+")", write
		case env.Overwrite:
			op.Symbol, op.Summary, op.Do = engine.Update, label+"   (--overwrite)", write
		case mine:
			op.Symbol, op.Summary, op.Items, op.Keep = engine.Conflict, label+"   edited by hand since Rigfile wrote it; not touched (use --overwrite)", nil, []state.Item{prev}
		default:
			op.Symbol, op.Summary, op.Items = engine.Conflict, label+"   already exists and is not managed by Rigfile; not touched (use --overwrite)", nil
		}
	}
	b.plan.Ops = append(b.plan.Ops, op)
}
