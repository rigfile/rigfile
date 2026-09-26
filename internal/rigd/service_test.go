package rigd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = os.Getenv("RIGFILE_UPDATE_GOLDEN") == "1"

func specFor(goos, home string) ServiceSpec {
	if goos == "windows" {
		return ServiceSpec{GOOS: goos, Home: home, Exe: `C:\Program Files\Rigfile\rigfile.exe`, UID: "501", StateDir: `C:\Users\me\AppData\Local\rigfile`, LogPath: `C:\Users\me\AppData\Local\rigfile\rigd\rigd.log`}
	}
	state := filepath.ToSlash(home) + "/.rigfile"
	return ServiceSpec{GOOS: goos, Home: filepath.ToSlash(home), Exe: "/usr/local/bin/rigfile", UID: "501", StateDir: state, LogPath: state + "/rigd/rigd.log"}
}

func TestServiceFiles(t *testing.T) {
	for goos, golden := range map[string]string{"darwin": "launchd.plist", "linux": "systemd.service", "windows": "task.xml"} {
		u, err := UnitFor(specFor(goos, "/home/me"))
		if err != nil {
			t.Fatal(err)
		}
		got := string(u.Content)
		if goos == "windows" {
			got = strings.ReplaceAll(DecodeUTF16(u.Content), "\r\n", "\n") // the golden file is stored with LF endings on every OS
			if u.Content[0] != 0xFF || u.Content[1] != 0xFE {
				t.Fatal("the task file must be UTF-16 with a byte-order mark")
			}
		}
		path := filepath.Join("testdata", "service", golden)
		if updateGolden {
			_ = os.WriteFile(path, []byte(got), 0o644)
		}
		want, err := os.ReadFile(path)
		if err != nil || string(want) != got {
			t.Errorf("%s differs from the golden file (RIGFILE_UPDATE_GOLDEN=1 to refresh):\n%s", goos, got)
		}
		// the definition runs `rigfile broker run` and nothing else, as the user, with no secrets or environment in it
		if !strings.Contains(got, "broker") || strings.Contains(strings.ToLower(got), "password") || strings.Contains(got, "SUDO") {
			t.Errorf("%s: %s", goos, got)
		}
	}
	// odd paths are quoted, not interpreted
	u, _ := UnitFor(ServiceSpec{GOOS: "linux", Home: "/h", Exe: `/opt/My "Tools"/100%/rigfile`, LogPath: "/h/l"})
	if !strings.Contains(string(u.Content), `ExecStart="/opt/My \"Tools\"/100%%/rigfile" broker run`) {
		t.Fatalf("%s", u.Content)
	}
	u, _ = UnitFor(ServiceSpec{GOOS: "darwin", Home: "/h", Exe: "/opt/a&b<c>/rigfile", LogPath: "/h/l"})
	if !strings.Contains(string(u.Content), "/opt/a&amp;b&lt;c&gt;/rigfile") {
		t.Fatalf("%s", u.Content)
	}
	if _, err := UnitFor(ServiceSpec{GOOS: "linux", Exe: "rigfile"}); err == nil {
		t.Fatal("a relative binary path would not survive a service manager")
	}
	if _, err := UnitFor(ServiceSpec{GOOS: "plan9", Exe: "/x"}); err == nil {
		t.Fatal("unknown OS")
	}
}

type fakeAct struct {
	ran  []string
	fail map[string]string // command prefix -> output; the command fails
}

func (f *fakeAct) Run(argv []string) (string, error) {
	line := strings.Join(argv, " ")
	f.ran = append(f.ran, line)
	for p, out := range f.fail {
		if strings.HasPrefix(line, p) {
			return out, errors.New("exit status 1")
		}
	}
	return "", nil
}

func newSvc(t *testing.T, goos string, act *fakeAct) *Service {
	t.Helper()
	home := t.TempDir()
	sp := specFor(goos, home)
	if goos == "windows" {
		sp.StateDir = filepath.Join(home, "state")
	}
	return &Service{Spec: sp, Act: act, BackupRoot: filepath.Join(home, "backups")}
}

func TestServiceInstallStartStopUninstall(t *testing.T) {
	for goos, want := range map[string][]string{
		"darwin":  {"launchctl bootout gui/501/com.rigfile.rigd", "launchctl bootstrap gui/501 "},
		"linux":   {"systemctl --user daemon-reload", "systemctl --user enable --now rigfile-rigd.service"},
		"windows": {"schtasks /Create /TN rigfile-rigd /XML ", "schtasks /Run /TN rigfile-rigd"},
	} {
		act := &fakeAct{}
		s := newSvc(t, goos, act)
		if s.Installed() {
			t.Fatal("nothing installed yet")
		}
		if err := s.Start(); err == nil {
			t.Fatal("starting an uninstalled service must say so")
		}
		path, err := s.Install()
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		if !s.Installed() {
			t.Fatalf("%s: the unit file must exist at %s", goos, path)
		}
		for i, w := range want {
			if i >= len(act.ran) || !strings.HasPrefix(act.ran[i], w) {
				t.Fatalf("%s: commands %q, want prefix %q at %d", goos, act.ran, w, i)
			}
		}
		act.ran = nil
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		if len(act.ran) < 2 {
			t.Fatalf("%q", act.ran)
		}
		act.ran = nil
		if err := s.Uninstall(); err != nil {
			t.Fatal(err)
		}
		if s.Installed() {
			t.Fatalf("%s: the unit file must be removed", goos)
		}
		if len(act.ran) == 0 {
			t.Fatal("uninstall must stop the service first")
		}
		// the journaled writer backed both writes up: two runs
		runs, _ := os.ReadDir(s.BackupRoot)
		if len(runs) != 2 {
			t.Fatalf("%s: %d journaled runs", goos, len(runs))
		}
	}
}

func TestServiceInstallRollsBackWhenActivationFails(t *testing.T) {
	act := &fakeAct{fail: map[string]string{"systemctl --user enable": "Failed to connect to bus: No medium found"}}
	s := newSvc(t, "linux", act)
	_, err := s.Install()
	if err == nil || !strings.Contains(err.Error(), "rolled back") || !strings.Contains(err.Error(), "No medium found") {
		t.Fatalf("%v", err)
	}
	if s.Installed() {
		t.Fatal("a failed install must leave the machine as it was")
	}
	// a re-install over a changed file backs the old one up
	act.fail = nil
	u, _ := UnitFor(s.Spec)
	_ = os.MkdirAll(filepath.Dir(u.Path), 0o755)
	_ = os.WriteFile(u.Path, []byte("user edited"), 0o644)
	if _, err := s.Install(); err != nil {
		t.Fatal(err)
	}
	found := false
	_ = filepath.Walk(s.BackupRoot, func(p string, fi os.FileInfo, _ error) error {
		if b, _ := os.ReadFile(p); fi != nil && !fi.IsDir() && string(b) == "user edited" {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("the file that was there before must be backed up")
	}
}
