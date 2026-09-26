package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitaldreamer3462/rigfile/internal/rigd"
)

// TestHelperLevel2 is the child launched by the Level 2 tests: it reports what its environment holds.
func TestHelperLevel2(t *testing.T) {
	if os.Getenv("RIGFILE_TEST_HELPER2") != "1" {
		return
	}
	ca, _ := os.ReadFile(os.Getenv("SSL_CERT_FILE"))
	out, _ := json.Marshal(map[string]any{
		"key": os.Getenv("ALPACA_API_KEY"), "proxy": os.Getenv("HTTPS_PROXY"), "node": os.Getenv("NODE_USE_ENV_PROXY"),
		"ca_is_cert":    strings.Contains(string(ca), "BEGIN CERTIFICATE") && !strings.Contains(string(ca), "PRIVATE"),
		"ca_vars_agree": os.Getenv("NODE_EXTRA_CA_CERTS") == os.Getenv("SSL_CERT_FILE") && os.Getenv("REQUESTS_CA_BUNDLE") == os.Getenv("SSL_CERT_FILE"),
		"ca_path":       os.Getenv("SSL_CERT_FILE"), "parent": os.Getenv("SPIKE_PARENT_ONLY"),
	})
	os.Stdout.Write(append(out, '\n'))
	os.Exit(0)
}

type l2rig struct {
	m    *machine
	dir  string
	real string
	b    *rigd.Broker
}

func newL2(t *testing.T, startBroker bool) *l2rig {
	t.Helper()
	m := newMachine(t)
	m.env["SPIKE_PARENT_ONLY"] = "should-not-leak"
	pass := filepath.Join(t.TempDir(), "pass")
	_ = os.WriteFile(pass, []byte("correct horse battery staple\n"), 0o600)
	m.env["RIGFILE_PASSPHRASE_FILE"] = pass
	r := &l2rig{m: m, dir: filepath.Join(m.stateDir(), "rigd"), real: "REAL" + "-" + "value" + "-" + "0d4c9b17aa"}
	if startBroker {
		r.b = &rigd.Broker{Dir: r.dir, Version: "test", Resolve: func(ref string) ([]byte, error) {
			if ref != "alpaca/api_key" {
				return nil, errors.New("not set")
			}
			return []byte(r.real), nil
		}}
		if err := r.b.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.b.Close() })
	}
	return r
}

func (r *l2rig) exec(extra ...string) result {
	args := append([]string{"exec", "--server", "alpaca", "--secret", "ALPACA_API_KEY=alpaca/api_key", "--env", "RIGFILE_TEST_HELPER2=1"}, extra...)
	args = append(args, "--", os.Args[0], "-test.run=^TestHelperLevel2$")
	return r.m.run("", args...)
}

var l2args = []string{"--allow", "api.alpaca.markets", "--bind", "alpaca/api_key=api.alpaca.markets"}

func TestExecLevel2GivesTheChildASurrogateAndEndsTheSession(t *testing.T) {
	r := newL2(t, true)
	if x := r.m.run("", "broker", "enable"); x.code != 0 {
		t.Fatalf("%+v", x)
	}
	res := r.exec(l2args...)
	if res.code != 0 {
		t.Fatalf("%+v", res)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.out)), &got); err != nil {
		t.Fatalf("%v: %q", err, res.out)
	}
	if !strings.HasPrefix(got["key"].(string), rigd.SurrogatePrefix) || got["ca_is_cert"] != true || got["ca_vars_agree"] != true || got["node"] != "1" || got["parent"] != "" {
		t.Fatalf("%v", got)
	}
	if !strings.HasPrefix(got["proxy"].(string), "http://s-") || !strings.HasSuffix(got["proxy"].(string), "@"+r.b.Info().Proxy) {
		t.Fatalf("%v", got["proxy"])
	}
	if strings.Contains(res.out+res.err, r.real) {
		t.Fatal("the real value reached the child or the output")
	}
	if r.b.Sessions() != 0 {
		t.Fatalf("the session must end when the child exits (%d live)", r.b.Sessions())
	}
	if _, err := os.Stat(got["ca_path"].(string)); err == nil {
		t.Fatal("the per-child CA file must be removed on exit")
	}
	if strings.Contains(res.err, "Level 1") {
		t.Fatalf("a Level 2 launch must not print a fallback notice: %q", res.err)
	}
}

func TestExecLevel2FallsBackOrFailsClosedAsSpecified(t *testing.T) {
	// not enabled: exactly the Level 1 behaviour (real value, no notice)
	r := newL2(t, false)
	if x := r.m.run("REAL-value-0d4c9b17aa\n", "secrets", "set", "alpaca/api_key"); x.code != 0 {
		t.Fatalf("%+v", x)
	}
	res := r.exec(l2args...)
	if res.code != 0 || !strings.Contains(res.out, `"key":"REAL-value-0d4c9b17aa"`) || strings.Contains(res.err, "broker") {
		t.Fatalf("disabled: %+v", res)
	}

	// enabled, server declares network.allow, broker not running: refuses to start the server
	r.m.run("", "broker", "enable")
	res = r.exec(l2args...)
	if res.code != 1 || !strings.Contains(res.err, "broker is not running") || !strings.Contains(res.err, "rigfile broker exclude alpaca") || strings.Contains(res.out, "REAL-value") {
		t.Fatalf("not running: %+v", res)
	}

	// enabled, no network.allow: Level 1 with a notice that says why
	res = r.exec()
	if res.code != 0 || !strings.Contains(res.err, "declares no network.allow") || !strings.Contains(res.out, `"key":"REAL-value-0d4c9b17aa"`) {
		t.Fatalf("no allow: %+v", res)
	}

	// enabled, excluded on purpose: Level 1 with a notice
	r.m.run("", "broker", "exclude", "alpaca")
	res = r.exec(l2args...)
	if res.code != 0 || !strings.Contains(res.err, "excluded from the broker") || !strings.Contains(res.out, `"key":"REAL-value-0d4c9b17aa"`) {
		t.Fatalf("excluded: %+v", res)
	}
}

func TestExecLevel2RefusesWhatItCannotProtect(t *testing.T) {
	r := newL2(t, true)
	r.m.run("", "broker", "enable")
	// a secret with no bound hosts: never falls back silently
	res := r.exec("--allow", "api.alpaca.markets")
	if res.code != 1 || !strings.Contains(res.err, "secrets.alpaca/api_key.hosts") || strings.Contains(res.out, "key") {
		t.Fatalf("%+v", res)
	}
	// a secret the broker cannot read
	res = r.m.run("", "exec", "--server", "alpaca", "--allow", "api.alpaca.markets", "--bind", "other/one=api.alpaca.markets", "--secret", "K=other/one", "--", os.Args[0], "-test.run=^TestHelperLevel2$")
	if res.code != 1 || !strings.Contains(res.err, "could not protect alpaca") || !strings.Contains(res.err, "broker exclude alpaca") {
		t.Fatalf("%+v", res)
	}
	// a malformed flag
	if res := r.m.run("", "exec", "--bind", "nohosts", "--", "x"); res.code != 2 {
		t.Fatalf("%+v", res)
	}
	if r.b.Sessions() != 0 {
		t.Fatal("a refused launch must leave no session behind")
	}
}

func TestLauncherHostsOnlyForPackageLaunchers(t *testing.T) {
	for cmd, want := range map[string]string{"npx": "registry.npmjs.org", `C:\x\npx.cmd`: "registry.npmjs.org", "uvx": "pypi.org", "/usr/bin/node": "", "python3": ""} {
		got := strings.Join(launcherHosts(cmd), ",")
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: %q", cmd, got)
		}
	}
}
