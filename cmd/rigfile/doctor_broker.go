package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/digitaldreamer3462/rigfile/internal/models"
	"github.com/digitaldreamer3462/rigfile/internal/rigd"
	"github.com/digitaldreamer3462/rigfile/internal/state"
)

// wrapped is what doctor learns from a `rigfile exec ...` MCP entry.
type wrapped struct {
	server            string
	secrets, hasAllow bool
}

// parseWrapper reads the flags of a wrapped entry's argv (see common.ExecWrapFor).
func parseWrapper(args []string) (wrapped, bool) {
	var w wrapped
	if len(args) == 0 || args[0] != "exec" {
		return w, false
	}
	for i := 1; i < len(args) && args[i] != "--"; i += 2 {
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--secret":
			w.secrets = true
		case "--allow":
			w.hasAllow = true
		case "--server":
			w.server = args[i+1]
		}
	}
	return w, true
}

// brokerLevels lists, for every applied MCP server that uses rigfile exec, which protection level a launch gets now.
func brokerLevels(st *state.State, dir string) (lines []string, worst checkLevel) {
	cfg, _ := rigd.LoadConfig(dir)
	running := false
	if c, err := rigd.ClientFromDir(dir); err == nil {
		if _, err := c.Status(); err == nil {
			running = true
		}
	}
	seen := map[string]string{}
	for _, ts := range st.Targets {
		for _, it := range ts.Items {
			if it.Category != "mcp" || it.Detail == nil {
				continue
			}
			var v struct {
				Args []string `json:"args"`
			}
			if json.Unmarshal([]byte(it.Detail["value"]), &v) != nil {
				continue
			}
			w, ok := parseWrapper(v.Args)
			if !ok {
				continue
			}
			name := w.server
			if name == "" {
				name = it.Key
			}
			seen[name] = levelFor(w, cfg, running)
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	worst = lvOK
	for _, n := range names {
		lines = append(lines, n+": "+seen[n])
		if strings.Contains(seen[n], "will refuse") {
			worst = lvFail
		} else if strings.HasPrefix(seen[n], "L1") && worst == lvOK {
			worst = lvWarn
		}
	}
	return lines, worst
}

func levelFor(w wrapped, cfg rigd.Config, running bool) string {
	switch {
	case !w.secrets:
		return "no secrets"
	case !cfg.Enabled:
		return "L1 (real key in the process; `rigfile broker enable` turns Level 2 on)"
	case cfg.IsExcluded(w.server):
		return "L1 (excluded from the broker; real key in the process)"
	case !w.hasAllow:
		return "L1 (real key in the process; the server declares no network.allow)"
	case !running:
		return "L2, broker not running: will refuse to start (rigfile broker run)"
	}
	return "L2 protected (surrogate key; real key stays in the broker)"
}

func addBrokerChecks(st *state.State, dir string, add func(checkLevel, string, string, ...any)) {
	lines, worst := brokerLevels(st, dir)
	if len(lines) == 0 {
		return
	}
	add(worst, "secret broker", "%s", strings.Join(lines, "; "))
}

// addSyncCheck reports the private-sync membership of this device (from its own settings; the secret store is not opened).
func addSyncCheck(sd string, add func(checkLevel, string, string, ...any)) {
	b, err := os.ReadFile(filepath.Join(syncDir(sd), "config.json"))
	if err != nil {
		return
	}
	var cfg syncConfig
	if json.Unmarshal(b, &cfg) != nil {
		add(lvWarn, "sync", "the sync settings are damaged")
		return
	}
	if _, err := os.Stat(cfg.Dir); err != nil {
		add(lvWarn, "sync", "the vault directory %s is not there (is the drive or folder available?)", cfg.Dir)
		return
	}
	add(lvOK, "sync", "device %s, vault %s, %d file(s) tracked", cfg.Device, cfg.Dir, len(cfg.Tracked))
}

// addModelChecks reports on every local model set up on this machine: server up, loopback only, chat, tool calls.
func addModelChecks(dir string, add func(checkLevel, string, string, ...any)) {
	recs, err := models.Load(dir)
	if err != nil || len(recs) == 0 {
		return
	}
	level := map[models.Level]checkLevel{models.OK: lvOK, models.Warn: lvWarn, models.Fail: lvFail}
	for _, n := range recs.Names() {
		r := recs[n]
		r.Name = n
		for _, c := range models.CheckModel(context.Background(), r, models.CheckOptions{}) {
			add(level[c.Level], "model "+n+": "+c.Name, "%s", c.Detail)
		}
	}
}
