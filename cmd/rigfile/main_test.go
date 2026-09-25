package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fixture = "../../testdata/fixtures/plan-example.rigfile.yaml"

type result struct {
	code     int
	out, err string
}

// invoke runs the CLI in-process with a fake HOME and a temp state dir.
func invoke(t *testing.T, stdin string, extraEnv map[string]string, args ...string) (result, string) {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, ".rigfile")
	ev := map[string]string{"HOME": home, "USERPROFILE": home, "PATH": os.Getenv("PATH")}
	for k, v := range extraEnv {
		ev[k] = v
	}
	var out, errb bytes.Buffer
	code := run(args, env{
		in: strings.NewReader(stdin), out: &out, err: &errb, stateDir: state,
		getenv: func(k string) string { return ev[k] }, keyringOff: true,
	})
	return result{code, out.String(), errb.String()}, home
}

func TestValidate(t *testing.T) {
	r, _ := invoke(t, "", nil, "validate", fixture)
	if r.code != 0 || !strings.Contains(r.out, "valid") {
		t.Fatalf("%+v", r)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("apiVersion: rigfile.dev/v1\nname: NOPE\nversion: 1.0.0\n"), 0o644)
	r, _ = invoke(t, "", nil, "validate", bad)
	if r.code != 1 || !strings.Contains(r.err, "invalid") {
		t.Fatalf("invalid manifest must exit 1 with problems: %+v", r)
	}
	r, _ = invoke(t, "", nil, "validate")
	if r.code != 2 {
		t.Fatalf("usage error must exit 2: %+v", r)
	}
}

func TestPlanApplyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	orig := "{\n  \"permissions\": {\n    \"deny\": [\n      \"Bash(git push*)\"\n    ]\n  },\n  \"model\": \"sonnet\"\n}\n"
	_ = os.WriteFile(settings, []byte(orig), 0o600)

	// plan: shows changes, writes nothing
	r, _ := invoke(t, "", nil, "plan", "claude", "--rig", fixture, "--settings", settings)
	if r.code != 0 || !strings.Contains(r.out, "+ deny  Read(~/.ssh/**)") || !strings.Contains(r.out, "change(s)") {
		t.Fatalf("plan output: %+v", r)
	}
	if got, _ := os.ReadFile(settings); string(got) != orig {
		t.Fatal("plan must not write")
	}

	// apply, answering "n": nothing changes
	r, _ = invoke(t, "n\n", nil, "apply", "claude", "--rig", fixture, "--settings", settings)
	if r.code != 1 {
		t.Fatalf("declined apply must exit 1: %+v", r)
	}
	if got, _ := os.ReadFile(settings); string(got) != orig {
		t.Fatal("declined apply must not write")
	}

	// apply --yes: writes, backs up, preserves the user's content and layout
	r, home := invoke(t, "", nil, "apply", "claude", "--rig", fixture, "--settings", settings, "--yes")
	if r.code != 0 || !strings.Contains(r.out, "backup:") || !strings.Contains(r.out, "applied") {
		t.Fatalf("apply: %+v", r)
	}
	got, _ := os.ReadFile(settings)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, got)
	}
	if !strings.HasPrefix(string(got), "{\n  \"permissions\": {\n    \"deny\": [\n      \"Bash(git push*)\"") || !strings.Contains(string(got), `"model": "sonnet"`) {
		t.Fatalf("existing content or layout lost:\n%s", got)
	}
	for _, want := range []string{`"Read(~/.ssh/**)"`, `"Read(**/secrets/**)"`, `"Read(~/.claude/.credentials.json)"`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
	// the backup holds the ORIGINAL bytes
	var backup string
	_ = filepath.Walk(filepath.Join(home, ".rigfile", "backups"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(p, string(filepath.Separator)+"files"+string(filepath.Separator)) {
			backup = p
		}
		return nil
	})
	if b, _ := os.ReadFile(backup); string(b) != orig {
		t.Fatalf("backup is not the original file (backup=%q)", backup)
	}

	// second run is a no-op
	r, _ = invoke(t, "", nil, "apply", "claude", "--rig", fixture, "--settings", settings, "--yes")
	if r.code != 0 || !strings.Contains(r.out, "no changes") {
		t.Fatalf("second apply should be a no-op: %+v", r)
	}
}

func TestApplyCreatesMissingSettings(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "new", "settings.json")
	r, _ := invoke(t, "", nil, "apply", "claude", "--rig", fixture, "--settings", settings, "--yes")
	if r.code != 0 || strings.Contains(r.out, "backup:") {
		t.Fatalf("%+v", r)
	}
	if b, err := os.ReadFile(settings); err != nil || !json.Valid(b) {
		t.Fatalf("settings not created: %v %s", err, b)
	}
}

func TestPlanRefusesInvalidRig(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "rig.yaml")
	_ = os.WriteFile(bad, []byte("apiVersion: rigfile.dev/v1\nname: a/b\nversion: 1.0.0\nunknown_key: 1\n"), 0o644)
	r, _ := invoke(t, "", nil, "plan", "claude", "--rig", bad, "--settings", filepath.Join(t.TempDir(), "s.json"))
	if r.code != 1 || !strings.Contains(r.err, "invalid") {
		t.Fatalf("%+v", r)
	}
	r, _ = invoke(t, "", nil, "plan", "claude")
	if r.code != 2 {
		t.Fatalf("missing flags must exit 2: %+v", r)
	}
}

func TestHookCommand(t *testing.T) {
	in := func(cmd string) string {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": cmd}})
		return string(b)
	}
	r, _ := invoke(t, in("git commit --no-verify -m x"), nil, "hook", "pre-tool-use")
	if r.code != 0 || !strings.Contains(r.out, `"permissionDecision":"deny"`) || !strings.Contains(r.out, `"hookEventName":"PreToolUse"`) {
		t.Fatalf("deny output: %+v", r)
	}
	r, _ = invoke(t, in("git status"), nil, "hook", "pre-tool-use")
	if r.code != 0 || r.out != "" {
		t.Fatalf("allowed command must produce no output: %+v", r)
	}
	r, _ = invoke(t, "this is not json", nil, "hook", "pre-tool-use")
	if r.code != 2 || !strings.Contains(r.err, "blocking") {
		t.Fatalf("malformed input must fail closed (exit 2): %+v", r)
	}
	r, _ = invoke(t, "{}", nil, "hook", "post-tool-use")
	if r.code != 2 {
		t.Fatalf("unsupported event: %+v", r)
	}
}

func writePassFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte("correct horse battery staple\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretsLifecycleWithFileBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file backend is stubbed on Windows until Stage 3")
	}
	pass := map[string]string{"RIGFILE_PASSPHRASE_FILE": writePassFile(t)}
	home := t.TempDir()
	state := filepath.Join(home, ".rigfile")
	call := func(stdin string, args ...string) result {
		var out, errb bytes.Buffer
		ev := map[string]string{"HOME": home, "RIGFILE_PASSPHRASE_FILE": pass["RIGFILE_PASSPHRASE_FILE"], "PATH": os.Getenv("PATH")}
		code := run(args, env{in: strings.NewReader(stdin), out: &out, err: &errb, stateDir: state,
			getenv: func(k string) string { return ev[k] }, keyringOff: true})
		return result{code, out.String(), errb.String()}
	}

	fakeValue := "FAKE-API-KEY-1234"
	r := call(fakeValue+"\n", "secrets", "set", "alpaca/api_key")
	if r.code != 0 || !strings.Contains(r.out, "encrypted-file") {
		t.Fatalf("set: %+v", r)
	}
	if strings.Contains(r.out+r.err, fakeValue) {
		t.Fatal("secret value echoed")
	}
	r = call("", "secrets", "status", "alpaca/api_key", "alpaca/missing")
	if r.code != 0 || !strings.Contains(r.out, "alpaca/api_key: set") || !strings.Contains(r.out, "alpaca/missing: not set") || strings.Contains(r.out+r.err, fakeValue) {
		t.Fatalf("status: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(state, "secrets.age")); bytes.Contains(b, []byte(fakeValue)) {
		t.Fatal("plaintext value in the secrets file")
	}
	r = call("", "secrets", "rm", "alpaca/api_key")
	if r.code != 0 {
		t.Fatalf("rm: %+v", r)
	}
	r = call("", "secrets", "status", "alpaca/api_key")
	if !strings.Contains(r.out, "not set") {
		t.Fatalf("still set after rm: %+v", r)
	}
	// input validation
	if r := call("v\n", "secrets", "set", "Bad Ref"); r.code == 0 {
		t.Fatal("invalid ref accepted")
	}
	if r := call("\n", "secrets", "set", "a/b"); r.code == 0 || !strings.Contains(r.err, "empty") {
		t.Fatalf("empty value accepted: %+v", r)
	}
	// a passphrase file that is world-readable is refused
	_ = os.Chmod(pass["RIGFILE_PASSPHRASE_FILE"], 0o644)
	if r := call("v\n", "secrets", "set", "a/b"); r.code == 0 || !strings.Contains(r.err, "chmod 600") {
		t.Fatalf("group/world-readable passphrase file must be refused: %+v", r)
	}
}

func TestSecretsNeedsPassphraseWhenHeadless(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	r, _ := invoke(t, "value\n", nil, "secrets", "set", "a/b")
	if r.code == 0 || !strings.Contains(r.err, "passphrase") {
		t.Fatalf("headless without a passphrase source must fail clearly: %+v", r)
	}
}

// TestHelperProcess is the child launched by the exec test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("RIGFILE_TEST_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("child sees ALPACA_API_KEY=" + os.Getenv("ALPACA_API_KEY") + " PAPER=" + os.Getenv("ALPACA_PAPER") +
		" LEAK=" + os.Getenv("SPIKE_PARENT_ONLY") + "\n")
	os.Exit(0)
}

func TestExecInjectsSecretsIntoChildOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file backend is stubbed on Windows until Stage 3")
	}
	t.Setenv("SPIKE_PARENT_ONLY", "should-not-leak")
	home := t.TempDir()
	state := filepath.Join(home, ".rigfile")
	pf := writePassFile(t)
	ev := map[string]string{"HOME": home, "RIGFILE_PASSPHRASE_FILE": pf, "PATH": os.Getenv("PATH"), "SPIKE_PARENT_ONLY": "should-not-leak"}
	call := func(stdin string, args ...string) result {
		var out, errb bytes.Buffer
		code := run(args, env{in: strings.NewReader(stdin), out: &out, err: &errb, stateDir: state,
			getenv: func(k string) string { return ev[k] }, keyringOff: true})
		return result{code, out.String(), errb.String()}
	}
	if r := call("FAKE-EXEC-VALUE\n", "secrets", "set", "alpaca/api_key"); r.code != 0 {
		t.Fatalf("set: %+v", r)
	}
	r := call("", "exec", "--secret", "ALPACA_API_KEY=alpaca/api_key", "--env", "ALPACA_PAPER=true", "--env", "RIGFILE_TEST_HELPER=1",
		"--", os.Args[0], "-test.run=^TestHelperProcess$")
	if r.code != 0 || !strings.Contains(r.out, "ALPACA_API_KEY=FAKE-EXEC-VALUE") || !strings.Contains(r.out, "PAPER=true") {
		t.Fatalf("child did not get its env: %+v", r)
	}
	if !strings.Contains(r.out, "LEAK=\n") {
		t.Fatalf("parent-only variable leaked into the child: %q", r.out)
	}
	// missing secret: fails before starting, with a helpful message
	r = call("", "exec", "--secret", "X=no/such", "--", os.Args[0])
	if r.code == 0 || !strings.Contains(r.err, "rigfile secrets set no/such") {
		t.Fatalf("%+v", r)
	}
	// usage errors
	for _, args := range [][]string{{"exec"}, {"exec", "--secret", "bad", "--", "x"}, {"exec", "--env", "=v", "--", "x"}} {
		if r := call("", args...); r.code != 2 {
			t.Fatalf("%v: want exit 2, got %+v", args, r)
		}
	}
}

func TestUnknownCommandAndUsage(t *testing.T) {
	if r, _ := invoke(t, "", nil); r.code != 2 {
		t.Fatalf("no args: %+v", r)
	}
	if r, _ := invoke(t, "", nil, "frobnicate"); r.code != 2 || !strings.Contains(r.err, "unknown command") {
		t.Fatalf("%+v", r)
	}
	if r, _ := invoke(t, "", nil, "version"); r.code != 0 || !strings.Contains(r.out, "rigfile") {
		t.Fatalf("%+v", r)
	}
	if r, _ := invoke(t, "", nil, "help"); r.code != 0 || !strings.Contains(r.out, "usage:") {
		t.Fatalf("%+v", r)
	}
}
