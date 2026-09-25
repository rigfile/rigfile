// Package platform is the ONLY place that knows about operating-system differences
// (CLAUDE.md working agreement 5, docs/platforms.md). Everything else asks it for paths and
// capabilities.
//
// Environment and OS are injected so the macOS, Linux and Windows logic can all be unit-tested on
// any host, and tests can point HOME at a temp dir (working agreement 4).
package platform

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// OS is the host operating-system family. WSL is a Linux sub-case (Info.WSL), not a separate OS.
type OS string

const (
	MacOS   OS = "macos"
	Linux   OS = "linux"
	Windows OS = "windows"
)

// ErrNotSupported is returned by capabilities that are stubbed on this OS (Windows, until Stage 3).
var ErrNotSupported = errors.New("platform: not yet supported on this OS")

// Options injects the environment. The zero value means "the real host".
type Options struct {
	GOOS     string                 // default runtime.GOOS
	GOARCH   string                 // default runtime.GOARCH
	Getenv   func(string) string    // default os.Getenv
	HomeFunc func() (string, error) // default os.UserHomeDir (ignored on Windows tests that set USERPROFILE)
}

// Info describes the host and resolves OS-specific locations.
type Info struct {
	OS   OS
	Arch string
	WSL  bool

	getenv func(string) string
	home   func() (string, error)
}

// New builds an Info. Unknown GOOS values are an error rather than a silent Linux fallback.
func New(o Options) (*Info, error) {
	goos := o.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	arch := o.GOARCH
	if arch == "" {
		arch = runtime.GOARCH
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	home := o.HomeFunc
	if home == nil {
		home = os.UserHomeDir
	}
	i := &Info{Arch: arch, getenv: getenv, home: home}
	switch goos {
	case "darwin":
		i.OS = MacOS
	case "linux":
		i.OS = Linux
		i.WSL = getenv("WSL_DISTRO_NAME") != "" // referenced in the Codex WSL docs; see docs/platforms.md §5
	case "windows":
		i.OS = Windows
	default:
		return nil, fmt.Errorf("platform: unsupported operating system %q", goos)
	}
	return i, nil
}

// Detect returns the real host.
func Detect() (*Info, error) { return New(Options{}) }

// Sep is the path separator of the target OS (independent of the OS running the tests).
func (i *Info) Sep() string {
	if i.OS == Windows {
		return `\`
	}
	return "/"
}

// Join joins path elements with the target OS's separator, without cleaning (no host-OS logic).
func (i *Info) Join(elem ...string) string {
	sep := i.Sep()
	var parts []string
	for n, e := range elem {
		if e == "" {
			continue
		}
		if n > 0 {
			e = strings.TrimLeft(e, `/\`)
		}
		if n < len(elem)-1 {
			e = strings.TrimRight(e, `/\`)
		}
		parts = append(parts, e)
	}
	return strings.Join(parts, sep)
}

// Home is the user's home directory ($HOME, or %USERPROFILE% on Windows).
func (i *Info) Home() (string, error) {
	if i.OS == Windows {
		if p := i.getenv("USERPROFILE"); p != "" {
			return p, nil
		}
		return "", errors.New("platform: USERPROFILE is not set")
	}
	if p := i.getenv("HOME"); p != "" {
		return p, nil
	}
	return i.home()
}

// ConfigDir is ${CONFIG_DIR}: where CLI tools' config conventionally lives.
// macOS ~/.config, Linux $XDG_CONFIG_HOME or ~/.config, Windows %APPDATA%.
// NOTE: Claude Code and Codex do NOT use this (they use ~/.claude and ~/.codex on every OS,
// docs/platforms.md §1); it is Rigfile's own config location and the base for ${CONFIG_DIR} in manifests.
func (i *Info) ConfigDir() (string, error) {
	switch i.OS {
	case Windows:
		if p := i.getenv("APPDATA"); p != "" {
			return p, nil
		}
		return "", errors.New("platform: APPDATA is not set")
	case Linux:
		if p := i.getenv("XDG_CONFIG_HOME"); p != "" {
			return p, nil
		}
	}
	h, err := i.Home()
	if err != nil {
		return "", err
	}
	return i.Join(h, ".config"), nil
}

// AppData is ${APP_DATA}: where GUI applications keep per-user config.
// macOS ~/Library/Application Support, Windows %APPDATA%, Linux: same as ConfigDir.
func (i *Info) AppData() (string, error) {
	switch i.OS {
	case MacOS:
		h, err := i.Home()
		if err != nil {
			return "", err
		}
		return i.Join(h, "Library", "Application Support"), nil
	default:
		return i.ConfigDir()
	}
}

// RigfileConfigDir is Rigfile's own config directory.
func (i *Info) RigfileConfigDir() (string, error) {
	c, err := i.ConfigDir()
	if err != nil {
		return "", err
	}
	return i.Join(c, "rigfile"), nil
}

// StateDir is where Rigfile keeps state.json, backups and the lockfile cache.
// macOS ~/.rigfile; Linux $XDG_STATE_HOME/rigfile if set, else ~/.rigfile; Windows %LOCALAPPDATA%\rigfile.
// (The plan uses two conventions on one OS; a tie-break is an open item in docs/platforms.md §7.)
func (i *Info) StateDir() (string, error) {
	switch i.OS {
	case Windows:
		if p := i.getenv("LOCALAPPDATA"); p != "" {
			return i.Join(p, "rigfile"), nil
		}
		return "", errors.New("platform: LOCALAPPDATA is not set")
	case Linux:
		if p := i.getenv("XDG_STATE_HOME"); p != "" {
			return i.Join(p, "rigfile"), nil
		}
	}
	h, err := i.Home()
	if err != nil {
		return "", err
	}
	return i.Join(h, ".rigfile"), nil
}

// Expand resolves manifest path variables in a canonical (forward-slash) path:
// a leading "~/" or ${HOME}/, ${CONFIG_DIR}/ and ${APP_DATA}/. The result uses the target OS's
// separator. Paths without a variable are returned unchanged (they are project-relative or system
// absolute; the adapter decides how to anchor them for its tool).
func (i *Info) Expand(p string) (string, error) {
	type v struct {
		prefix string
		fn     func() (string, error)
	}
	vars := []v{
		{"~/", i.Home}, {"${HOME}/", i.Home},
		{"${CONFIG_DIR}/", i.ConfigDir}, {"${APP_DATA}/", i.AppData},
	}
	for _, x := range vars {
		if strings.HasPrefix(p, x.prefix) {
			base, err := x.fn()
			if err != nil {
				return "", err
			}
			rest := strings.TrimPrefix(p, x.prefix)
			if i.OS == Windows {
				rest = strings.ReplaceAll(rest, "/", `\`)
			}
			return i.Join(base, rest), nil
		}
	}
	if strings.Contains(p, "${") {
		return "", fmt.Errorf("platform: unknown or misplaced variable in %q", p)
	}
	return p, nil
}

// SecretStoreKind names the preferred secret backend for this OS.
type SecretStoreKind string

const (
	StoreKeychain          SecretStoreKind = "macos-keychain"
	StoreSecretService     SecretStoreKind = "secret-service"
	StoreCredentialManager SecretStoreKind = "windows-credential-manager"
)

// PreferredSecretStore returns the OS-native store. Whether it is *available* (e.g. a headless
// Linux box has no Secret Service) is checked by internal/secrets at run time.
func (i *Info) PreferredSecretStore() SecretStoreKind {
	switch i.OS {
	case MacOS:
		return StoreKeychain
	case Windows:
		return StoreCredentialManager
	default:
		return StoreSecretService
	}
}
