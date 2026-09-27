package svc

import (
	"errors"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func spec(goos string) Spec {
	s := Spec{GOOS: goos, Home: "/home/me", UID: "501", StateDir: "/home/me/.rigfile", Name: "model-local-coder",
		Description: "Local model server (local-coder)", Exe: "/usr/local/bin/ollama", Args: []string{"serve"},
		Env: map[string]string{"OLLAMA_HOST": "127.0.0.1:11434", "AAA": "1"}, LogPath: "/home/me/.rigfile/models/local-coder.log"}
	if goos == "windows" {
		s.Home, s.StateDir, s.Exe, s.LogPath = `C:\Users\me`, `C:\Users\me\AppData\Local\rigfile`, `C:\Program Files\Ollama\ollama.exe`, `C:\Users\me\AppData\Local\rigfile\models\local-coder.log`
	}
	return s
}

func TestUnitsCarryArgumentsAndEnvironment(t *testing.T) {
	u, err := UnitFor(spec("darwin"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(u.Content)
	for _, want := range []string{"<string>com.rigfile.model-local-coder</string>", "<string>/usr/local/bin/ollama</string>\n    <string>serve</string>", "<key>EnvironmentVariables</key>", "<key>AAA</key>\n    <string>1</string>", "<key>OLLAMA_HOST</key>\n    <string>127.0.0.1:11434</string>"} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(filepath.ToSlash(u.Path), "Library/LaunchAgents/com.rigfile.model-local-coder.plist") {
		t.Fatal(u.Path)
	}
	u, _ = UnitFor(spec("linux"))
	got = string(u.Content)
	if !strings.Contains(got, `ExecStart="/usr/local/bin/ollama" serve`) || !strings.Contains(got, `Environment="AAA=1"`) || !strings.Contains(got, `Environment="OLLAMA_HOST=127.0.0.1:11434"`) || !strings.HasSuffix(filepath.ToSlash(u.Path), "rigfile-model-local-coder.service") {
		t.Fatalf("%s", got)
	}
	// environment is written in a stable order
	if strings.Index(got, "AAA=1") > strings.Index(got, "OLLAMA_HOST") {
		t.Fatal("sorted")
	}
	u, _ = UnitFor(spec("windows"))
	got = html.UnescapeString(DecodeUTF16(u.Content))
	if !strings.Contains(got, `<Command>cmd.exe</Command>`) || !strings.Contains(got, `/d /s /c "set "AAA=1"&& set "OLLAMA_HOST=127.0.0.1:11434"&& "C:\Program Files\Ollama\ollama.exe" serve"`) {
		t.Fatalf("%s", got)
	}
	// without environment the task runs the program directly
	w := spec("windows")
	w.Env = nil
	u, _ = UnitFor(w)
	if got := DecodeUTF16(u.Content); !strings.Contains(got, `<Command>C:\Program Files\Ollama\ollama.exe</Command>`) || !strings.Contains(got, "<Arguments>serve</Arguments>") {
		t.Fatalf("%s", got)
	}
}

func TestUnsafeValuesAreRefused(t *testing.T) {
	for name, mut := range map[string]func(*Spec){
		"a relative program":        func(s *Spec) { s.Exe = "ollama" },
		"a bad name":                func(s *Spec) { s.Name = "Bad Name" },
		"an env value with a quote": func(s *Spec) { s.Env = map[string]string{"K": `a"b`} },
		"an env value with a space": func(s *Spec) { s.Env = map[string]string{"K": "a b"} },
		"a bad env name":            func(s *Spec) { s.Env = map[string]string{"1K": "a"} },
	} {
		s := spec("linux")
		mut(&s)
		if _, err := UnitFor(s); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	for _, bad := range []string{`--x="y"`, "a&b", "a|b", "100%", "a^b", "a>b"} {
		s := spec("windows")
		s.Args = []string{bad}
		if _, err := UnitFor(s); err == nil {
			t.Errorf("windows argument %q must be refused (cmd metacharacters)", bad)
		}
	}
	// the same characters are fine where no cmd wrapper exists
	s := spec("linux")
	s.Args = []string{"a&b"}
	if _, err := UnitFor(s); err != nil {
		t.Errorf("%v", err)
	}
}

type act struct {
	ran  []string
	fail string
}

func (a *act) Run(argv []string) (string, error) {
	line := strings.Join(argv, " ")
	a.ran = append(a.ran, line)
	if a.fail != "" && strings.HasPrefix(line, a.fail) {
		return "boom", errors.New("exit 1")
	}
	return "", nil
}

func TestInstallStartStopUninstallForAnotherService(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		home := t.TempDir()
		s := spec(goos)
		s.Home = home
		s.StateDir = filepath.Join(home, "state")
		s.Exe = map[string]string{"windows": `C:\o\ollama.exe`}[goos]
		if s.Exe == "" {
			s.Exe = "/usr/bin/ollama"
		}
		a := &act{}
		sv := &Service{Spec: s, Act: a, BackupRoot: filepath.Join(home, "backups")}
		if sv.Installed() {
			t.Fatal("not yet")
		}
		path, err := sv.Install()
		if err != nil || !sv.Installed() {
			t.Fatalf("%s: %v", goos, err)
		}
		if !strings.Contains(strings.Join(a.ran, "|"), "model-local-coder") {
			t.Fatalf("%s: the service manager must be told the service's own name: %v", goos, a.ran)
		}
		if err := sv.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := sv.Start(); err != nil {
			t.Fatal(err)
		}
		if err := sv.Uninstall(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s: unit file must be removed", goos)
		}
		// a failed activation rolls the file back
		a.fail = map[string]string{"darwin": "launchctl bootstrap", "linux": "systemctl --user enable", "windows": "schtasks /Create"}[goos]
		if _, err := sv.Install(); err == nil || !strings.Contains(err.Error(), "rolled back") || sv.Installed() {
			t.Fatalf("%s: %v", goos, err)
		}
	}
}
