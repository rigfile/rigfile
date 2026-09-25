package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func mk(t *testing.T, goos string, e map[string]string) *Info {
	t.Helper()
	i, err := New(Options{GOOS: goos, GOARCH: "arm64", Getenv: env(e)})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestDetectOS(t *testing.T) {
	for goos, want := range map[string]OS{"darwin": MacOS, "linux": Linux, "windows": Windows} {
		if got := mk(t, goos, nil).OS; got != want {
			t.Errorf("%s: got %s want %s", goos, got, want)
		}
	}
	if _, err := New(Options{GOOS: "plan9"}); err == nil {
		t.Error("unknown OS must be an error, not a silent Linux fallback")
	}
}

func TestWSLDetection(t *testing.T) {
	if !mk(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}).WSL {
		t.Error("WSL_DISTRO_NAME should mark WSL")
	}
	if mk(t, "linux", nil).WSL {
		t.Error("plain Linux is not WSL")
	}
	if mk(t, "windows", map[string]string{"WSL_DISTRO_NAME": "x"}).WSL {
		t.Error("WSL is a Linux sub-case only")
	}
}

func TestPathsPerOS(t *testing.T) {
	type want struct{ home, config, appdata, state string }
	cases := map[string]struct {
		env  map[string]string
		want want
	}{
		"darwin": {
			map[string]string{"HOME": "/Users/test"},
			want{"/Users/test", "/Users/test/.config", "/Users/test/Library/Application Support", "/Users/test/.rigfile"},
		},
		"linux": {
			map[string]string{"HOME": "/home/test"},
			want{"/home/test", "/home/test/.config", "/home/test/.config", "/home/test/.rigfile"},
		},
		"linux-xdg": {
			map[string]string{"HOME": "/home/test", "XDG_CONFIG_HOME": "/xdg/c", "XDG_STATE_HOME": "/xdg/s"},
			want{"/home/test", "/xdg/c", "/xdg/c", "/xdg/s/rigfile"},
		},
		"windows": {
			map[string]string{"USERPROFILE": `C:\Users\test`, "APPDATA": `C:\Users\test\AppData\Roaming`, "LOCALAPPDATA": `C:\Users\test\AppData\Local`},
			want{`C:\Users\test`, `C:\Users\test\AppData\Roaming`, `C:\Users\test\AppData\Roaming`, `C:\Users\test\AppData\Local\rigfile`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			goos := map[string]string{"darwin": "darwin", "linux": "linux", "linux-xdg": "linux", "windows": "windows"}[name]
			i := mk(t, goos, c.env)
			check := func(label string, f func() (string, error), want string) {
				got, err := f()
				if err != nil || got != want {
					t.Errorf("%s: got %q err=%v, want %q", label, got, err, want)
				}
			}
			check("Home", i.Home, c.want.home)
			check("ConfigDir", i.ConfigDir, c.want.config)
			check("AppData", i.AppData, c.want.appdata)
			check("StateDir", i.StateDir, c.want.state)
		})
	}
}

func TestWindowsMissingEnvIsAnError(t *testing.T) {
	i := mk(t, "windows", nil)
	for label, f := range map[string]func() (string, error){"Home": i.Home, "ConfigDir": i.ConfigDir, "StateDir": i.StateDir} {
		if _, err := f(); err == nil {
			t.Errorf("%s: expected an error when the env var is missing", label)
		}
	}
}

func TestExpand(t *testing.T) {
	mac := mk(t, "darwin", map[string]string{"HOME": "/Users/test"})
	win := mk(t, "windows", map[string]string{"USERPROFILE": `C:\Users\test`, "APPDATA": `C:\Users\test\AppData\Roaming`})
	tests := []struct {
		i    *Info
		in   string
		want string
		err  bool
	}{
		{mac, "~/.ssh/**", "/Users/test/.ssh/**", false},
		{mac, "${HOME}/x", "/Users/test/x", false},
		{mac, "${CONFIG_DIR}/rigfile/gitignore", "/Users/test/.config/rigfile/gitignore", false},
		{mac, "${APP_DATA}/Claude/claude_desktop_config.json", "/Users/test/Library/Application Support/Claude/claude_desktop_config.json", false},
		{win, "~/.ssh/config", `C:\Users\test\.ssh\config`, false},
		{win, "${CONFIG_DIR}/rigfile/gitignore", `C:\Users\test\AppData\Roaming\rigfile\gitignore`, false},
		{win, "${APP_DATA}/Claude/claude_desktop_config.json", `C:\Users\test\AppData\Roaming\Claude\claude_desktop_config.json`, false},
		{mac, "**/.env", "**/.env", false},                         // project-relative: unchanged
		{mac, "/etc/ssl/private/**", "/etc/ssl/private/**", false}, // system absolute: unchanged
		{mac, "${NOPE}/x", "", true},
		{mac, "a/${HOME}/b", "", true}, // variable not at the start
	}
	for _, tc := range tests {
		got, err := tc.i.Expand(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("Expand(%q) = %q, %v; want %q (err=%v)", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestPreferredSecretStore(t *testing.T) {
	if mk(t, "darwin", nil).PreferredSecretStore() != StoreKeychain ||
		mk(t, "linux", nil).PreferredSecretStore() != StoreSecretService ||
		mk(t, "windows", nil).PreferredSecretStore() != StoreCredentialManager {
		t.Error("wrong preferred secret store")
	}
}

func TestWritePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		if err := WritePrivate(filepath.Join(t.TempDir(), "x"), []byte("v")); err != ErrNotSupported {
			t.Fatalf("Windows must fail closed until implemented, got %v", err)
		}
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "secret.age")
	if err := WritePrivate(p, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v err=%v, want 0600", st.Mode().Perm(), err)
	}
	dst, _ := os.Stat(filepath.Dir(p))
	if dst.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dst.Mode().Perm())
	}
	// Overwrite keeps 0600 and leaves no temp files behind.
	if err := WritePrivate(p, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "v2" {
		t.Fatalf("content = %q", b)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestSafeRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		got, err := SafeRelative(`C:\Users\test\.claude\settings.json`)
		if err != nil || got != `C_\Users\test\.claude\settings.json` {
			t.Fatalf("got %q err=%v", got, err)
		}
	} else {
		got, err := SafeRelative("/Users/test/.claude/settings.json")
		if err != nil || got != "Users/test/.claude/settings.json" {
			t.Fatalf("got %q err=%v", got, err)
		}
		// Cleaning removes traversal before it can escape a backup root.
		got, err = SafeRelative("/a/b/../../c")
		if err != nil || got != "c" {
			t.Fatalf("got %q err=%v", got, err)
		}
	}
	if _, err := SafeRelative("relative/path"); err == nil {
		t.Fatal("relative paths must be rejected")
	}
}
