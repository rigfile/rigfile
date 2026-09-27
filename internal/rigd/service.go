package rigd

import (
	"path/filepath"

	"github.com/digitaldreamer3462/rigfile/internal/svc"
)

// Service names, one per OS convention (they are what internal/svc derives from the name "rigd").
const (
	LaunchdLabel = "com.rigfile.rigd"
	SystemdUnit  = "rigfile-rigd.service"
	TaskName     = "rigfile-rigd"
)

// ServiceSpec is everything the generated files depend on. Nothing here comes from the environment, so the files are
// reproducible in tests.
type ServiceSpec struct {
	GOOS     string // darwin, linux, windows
	Home     string
	Exe      string // absolute path of the rigfile binary
	LogPath  string // where the service's stdout/stderr go
	UID      string // numeric user id (launchd's gui/<uid> domain)
	StateDir string
}

// Unit is one generated service definition.
type Unit = svc.Unit

// Activator runs the OS's service-manager commands. Tests replace it; the real one runs them.
type Activator = svc.Activator

func (s ServiceSpec) spec() svc.Spec {
	return svc.Spec{
		GOOS: s.GOOS, Home: s.Home, UID: s.UID, StateDir: s.StateDir, Name: "rigd",
		Description: "Rigfile secret broker (rigd)",
		Docs:        "https://github.com/digitaldreamer3462/rigfile/blob/main/docs/rigd.md",
		Exe:         s.Exe, Args: []string{"broker", "run"}, LogPath: s.LogPath,
		TaskFile: filepath.Join(s.StateDir, "rigd", "rigd-task.xml"),
	}
}

// UnitFor renders the service definition for the spec's OS.
func UnitFor(s ServiceSpec) (Unit, error) { return svc.UnitFor(s.spec()) }

// DecodeUTF16 turns the Windows task file back into text.
func DecodeUTF16(b []byte) string { return svc.DecodeUTF16(b) }

// Service installs, removes and drives the broker service.
type Service struct {
	Spec       ServiceSpec
	Act        Activator
	BackupRoot string // Rigfile's backup directory: the unit file is written through the journaled writer
}

func (s *Service) inner() *svc.Service {
	return &svc.Service{Spec: s.Spec.spec(), Act: s.Act, BackupRoot: s.BackupRoot, InstallNote: "rigfile broker install", UninstallNote: "rigfile broker uninstall"}
}

// Install writes the unit through the journaled writer, then activates it; a failed activation rolls the write back.
func (s *Service) Install() (string, error) { return s.inner().Install() }

// Uninstall stops and disables the service and removes its unit file.
func (s *Service) Uninstall() error { return s.inner().Uninstall() }

// Installed reports whether the unit file is there.
func (s *Service) Installed() bool { return s.inner().Installed() }

// Start and Stop drive an installed service.
func (s *Service) Start() error { return s.inner().Start() }
func (s *Service) Stop() error  { return s.inner().Stop() }
