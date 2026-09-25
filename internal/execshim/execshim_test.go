package execshim

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
	"github.com/digitaldreamer3462/rigfile/internal/secrets"
)

// TestHelperProcess is not a real test: it is the child program that Run launches. It prints its
// entire environment as JSON, or exits with a chosen code.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("RIGFILE_TEST_HELPER") != "1" {
		return
	}
	if code := os.Getenv("RIGFILE_TEST_EXIT"); code != "" {
		if code == "7" {
			os.Exit(7)
		}
	}
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(m)
	os.Exit(0)
}

func host(t *testing.T) *platform.Info {
	t.Helper()
	p, err := platform.Detect()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func helperCmd() []string { return []string{os.Args[0], "-test.run=^TestHelperProcess$"} }

func run(t *testing.T, s Spec) (map[string]string, int, error) {
	t.Helper()
	var out, errb bytes.Buffer
	s.Command = helperCmd()
	s.Plat = host(t)
	s.Stdout, s.Stderr = &out, &errb
	code, err := Run(context.Background(), s)
	env := map[string]string{}
	if out.Len() > 0 {
		_ = json.Unmarshal(out.Bytes(), &env)
	}
	return env, code, err
}

func TestChildGetsSecretsAndOnlyDeclaredEnvironment(t *testing.T) {
	keyring.MockInit()
	st := secrets.KeyringStore{}
	if err := st.Set("alpaca/api_key", []byte("FAKE-KEY-VALUE")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNRELATED_PARENT_SECRET", "must-not-leak")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-leak-either")

	env, code, err := run(t, Spec{
		Store:   st,
		Secrets: map[string]string{"ALPACA_API_KEY": "alpaca/api_key"},
		Literal: map[string]string{"ALPACA_PAPER": "true", "RIGFILE_TEST_HELPER": "1"},
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if env["ALPACA_API_KEY"] != "FAKE-KEY-VALUE" || env["ALPACA_PAPER"] != "true" {
		t.Fatalf("declared variables missing: %v", env)
	}
	for _, leaked := range []string{"UNRELATED_PARENT_SECRET", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := env[leaked]; ok {
			t.Fatalf("%s leaked into the child environment", leaked)
		}
	}
	if env["PATH"] == "" {
		t.Fatal("PATH (base environment) missing")
	}
}

func TestMissingSecretAbortsBeforeStartWithoutLeakingAnything(t *testing.T) {
	keyring.MockInit()
	_, code, err := run(t, Spec{
		Store:   secrets.KeyringStore{},
		Secrets: map[string]string{"X_KEY": "nope/missing"},
		Literal: map[string]string{"RIGFILE_TEST_HELPER": "1"},
	})
	if err == nil || code == 0 {
		t.Fatalf("expected failure, got code=%d err=%v", code, err)
	}
	if !strings.Contains(err.Error(), "nope/missing") || !strings.Contains(err.Error(), "rigfile secrets set") {
		t.Fatalf("error should name the ref and the fix: %v", err)
	}
}

func TestExitCodeIsPropagated(t *testing.T) {
	_, code, err := run(t, Spec{Literal: map[string]string{"RIGFILE_TEST_HELPER": "1", "RIGFILE_TEST_EXIT": "7"}})
	if err != nil || code != 7 {
		t.Fatalf("code=%d err=%v, want 7", code, err)
	}
}

func TestBadEnvNamesAndNoCommand(t *testing.T) {
	p := host(t)
	if _, err := BuildEnv(Spec{Plat: p, Literal: map[string]string{"BAD NAME": "x"}}); err == nil {
		t.Error("invalid env name must be rejected")
	}
	if _, err := BuildEnv(Spec{Plat: p, Secrets: map[string]string{"1BAD": "a/b"}}); err == nil {
		t.Error("invalid env name must be rejected")
	}
	if code, err := Run(context.Background(), Spec{Plat: p}); err == nil || code == 0 {
		t.Error("empty command must fail")
	}
	code, err := Run(context.Background(), Spec{Plat: p, Command: []string{filepath.Join(t.TempDir(), "does-not-exist")}})
	if err == nil || code != 127 {
		t.Errorf("missing binary: code=%d err=%v, want 127", code, err)
	}
}

func TestBuildEnvIsDeterministicAndSorted(t *testing.T) {
	p := host(t)
	a, _ := BuildEnv(Spec{Plat: p, Getenv: func(string) string { return "" }, Literal: map[string]string{"B": "2", "A": "1", "C": "3"}})
	b, _ := BuildEnv(Spec{Plat: p, Getenv: func(string) string { return "" }, Literal: map[string]string{"C": "3", "A": "1", "B": "2"}})
	if strings.Join(a, "|") != strings.Join(b, "|") || strings.Join(a, "|") != "A=1|B=2|C=3" {
		t.Fatalf("a=%v b=%v", a, b)
	}
}
