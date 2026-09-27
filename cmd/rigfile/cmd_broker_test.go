package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/state"
)

func TestBrokerCommandsKeepTheChoiceAndReportStatus(t *testing.T) {
	m := newMachine(t)
	if r := m.run("", "broker", "status"); r.code != 1 || !strings.Contains(r.out, "Level 2: off") || !strings.Contains(r.out, "not running") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "enable"); r.code != 0 || !strings.Contains(r.out, "Level 2 is on") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "exclude", "legacy-tool"); r.code != 0 || !strings.Contains(r.out, "Level 1") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "status"); !strings.Contains(r.out, "Level 2: on") || !strings.Contains(r.out, "excluded: legacy-tool") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "include", "legacy-tool"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "status"); strings.Contains(r.out, "excluded") {
		t.Fatalf("%+v", r)
	}
	if r := m.run("", "broker", "disable"); r.code != 0 || !strings.Contains(r.out, "Level 2 is off") {
		t.Fatalf("%+v", r)
	}
	for _, a := range [][]string{{"broker"}, {"broker", "nope"}, {"broker", "exclude"}} {
		if r := m.run("", a...); r.code != 2 {
			t.Fatalf("%v: %+v", a, r)
		}
	}
}

type recordingAct struct{ ran []string }

func (r *recordingAct) Run(argv []string) (string, error) {
	r.ran = append(r.ran, strings.Join(argv, " "))
	return "", nil
}

func TestBrokerInstallWritesTheUnitAndUsesTheServiceManager(t *testing.T) {
	m := newMachine(t)
	act := &recordingAct{}
	exe := "/usr/local/bin/rigfile"
	if runtime.GOOS == "windows" {
		exe = `C:\Program Files\Rigfile\rigfile.exe`
	}
	run := func(a ...string) result {
		var out, errb bytes.Buffer
		code := runWith(m, &out, &errb, func(e *env) { e.brokerAct, e.exe = act, exe }, a...)
		return result{code, portable(out.String()), portable(errb.String())}
	}
	r := run("broker", "install")
	if r.code != 0 || !strings.Contains(r.out, "Installed the broker service") {
		t.Fatalf("%+v", r)
	}
	if len(act.ran) == 0 {
		t.Fatal("the service manager was not asked to start it")
	}
	if r := run("broker", "stop"); r.code != 0 || !strings.Contains(r.out, "Stopped the broker service") {
		t.Fatalf("%+v", r)
	}
	if r := run("broker", "start"); r.code != 0 || !strings.Contains(r.out, "Started the broker service") {
		t.Fatalf("%+v", r)
	}
	if r := run("broker", "uninstall"); r.code != 0 || !strings.Contains(r.out, "Removed") {
		t.Fatalf("%+v", r)
	}
	// nothing installed any more: `stop` finds no service and no background broker
	if r := run("broker", "stop"); r.code != 0 || !strings.Contains(r.out, "not running") {
		t.Fatalf("%+v", r)
	}
	// the writes went through the journal, so `rigfile rollback --list` shows them
	if r := m.run("", "rollback", "--list"); !strings.Contains(r.out+r.err, "broker") {
		t.Fatalf("%+v", r)
	}
}

// TestBrokerBackgroundStartServesLevel2 runs the real thing end to end: a detached `rigfile broker run` process (this test
// binary acting as the CLI), a secret in the file-backed store, and `rigfile exec` getting a surrogate from it.
func TestBrokerBackgroundStartServesLevel2(t *testing.T) {
	m := newMachine(t)
	pass := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	// the detached process reads the real environment: give it the same temp machine, and never the OS keychain
	for k, v := range map[string]string{"HOME": m.env["HOME"], "USERPROFILE": m.env["USERPROFILE"], "APPDATA": m.env["APPDATA"], "LOCALAPPDATA": m.env["LOCALAPPDATA"],
		"RIGFILE_SECRETS_BACKEND": "file", "RIGFILE_PASSPHRASE_FILE": pass, "RIGFILE_TEST_AS_CLI": "1", "XDG_STATE_HOME": ""} {
		t.Setenv(k, v)
	}
	m.env["RIGFILE_SECRETS_BACKEND"] = "file"
	real := "REAL" + "-" + "value" + "-" + "5e11b0c7d9"
	if r := m.run(real+"\n", "secrets", "set", "alpaca/api_key"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	m.run("", "broker", "enable")
	// what an apply approved: the background broker reads it from state.json
	st := state.New()
	st.Broker = map[string]state.ServerPolicy{"alpaca": {Command: "node", Allow: []string{"api.alpaca.markets"}, Secrets: map[string]state.SecretBinding{"ALPACA_API_KEY": {Ref: "alpaca/api_key", Hosts: []string{"api.alpaca.markets"}}}}}
	sb, _ := st.Marshal()
	if err := os.WriteFile(filepath.Join(m.stateDir(), state.FileName), sb, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	start := func() int {
		out.Reset()
		errb.Reset()
		return runWith(m, &out, &errb, func(e *env) { e.exe = os.Args[0] }, "broker", "start")
	}
	if code := start(); code != 0 {
		t.Fatalf("start: %d %s %s", code, out.String(), errb.String())
	}
	t.Cleanup(func() { m.run("", "broker", "stop") })
	if code := start(); code != 0 || !strings.Contains(out.String(), "already running") {
		t.Fatalf("a second start must be a no-op: %d %s", code, out.String())
	}
	if r := m.run("", "broker", "status"); r.code != 0 || !strings.Contains(r.out, "broker: running") {
		t.Fatalf("%+v", r)
	}
	res := m.run("", "exec", "--server", "alpaca", "--allow", "api.alpaca.markets", "--bind", "alpaca/api_key=api.alpaca.markets",
		"--secret", "ALPACA_API_KEY=alpaca/api_key", "--env", "RIGFILE_TEST_HELPER2=1", "--", os.Args[0], "-test.run=^TestHelperLevel2$")
	if res.code != 0 || !strings.Contains(res.out, `"key":"rgs_sur_`) || strings.Contains(res.out+res.err, real) {
		t.Fatalf("%+v", res)
	}
	if r := m.run("", "broker", "stop"); r.code != 0 || !strings.Contains(r.out, "Stopped the broker") {
		t.Fatalf("%+v", r)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := m.run("", "broker", "status"); strings.Contains(r.out, "not running") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the broker did not stop")
}

func TestApplyApprovesTheBrokerPolicyFromTheRig(t *testing.T) {
	m := newMachine(t)
	rig := plainRig(t, "mcp_servers:\n  alpaca:\n    command: npx\n    args: [\"-y\", \"alpaca-mcp@1.4.2\"]\n    env: {ALPACA_API_KEY: \"secret://alpaca/api_key\", MODE: paper}\n    network: {allow: [api.alpaca.markets]}\n  nopolicy:\n    command: npx\n    args: [\"-y\", \"other-mcp@1.0.0\"]\nsecrets:\n  alpaca/api_key: {description: key, hosts: [api.alpaca.markets]}\n")
	if r := m.run("", "apply", rig, "--yes", "--no-git"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	st, err := state.Load(m.stateDir())
	if err != nil {
		t.Fatal(err)
	}
	pol, ok := st.Broker["alpaca"]
	if !ok || pol.Command != "npx" || len(pol.Allow) != 1 || pol.Allow[0] != "api.alpaca.markets" || pol.Secrets["ALPACA_API_KEY"].Ref != "alpaca/api_key" || pol.Secrets["ALPACA_API_KEY"].Hosts[0] != "api.alpaca.markets" || len(pol.Secrets) != 1 {
		t.Fatalf("%+v", st.Broker)
	}
	if _, ok := st.Broker["nopolicy"]; ok {
		t.Fatal("a server that declares no network.allow has nothing to enforce, so no policy")
	}
}
