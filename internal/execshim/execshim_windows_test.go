//go:build windows

package execshim

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// npx and npm are .cmd shims on Windows. The wrapper must launch them (Claude Code's exec form needs a real
// .exe, which is rigfile itself) and hand them the injected environment.
func TestCmdShimIsFoundThroughPathextAndGetsTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fakeshim.cmd"), []byte("@echo off\r\necho token=%FAKE_TOKEN% arg=%1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	pi := host(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code, err := Run(context.Background(), Spec{
		Command: []string{"fakeshim", "hello"}, Literal: map[string]string{"FAKE_TOKEN": "fake-value"},
		Plat: pi, Stdout: &out, Stderr: &errb,
	})
	if err != nil || code != 0 || !strings.Contains(out.String(), "token=fake-value arg=hello") {
		t.Fatalf("code=%d err=%v out=%q err=%q", code, err, out.String(), errb.String())
	}
}

func TestUnsafeArgumentsToCmdShimAreNeverInterpretedAsCommands(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "fakeshim.cmd"), []byte("@echo off\r\necho got\r\n"), 0o644)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	_, _ = Run(context.Background(), Spec{Command: []string{"fakeshim", "x & echo INJECTED"}, Plat: host(t), Stdout: &out, Stderr: &out})
	for _, l := range strings.Split(strings.ReplaceAll(out.String(), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) == "INJECTED" {
			t.Fatalf("an argument was executed as a command:\n%s", out.String())
		}
	}
}
