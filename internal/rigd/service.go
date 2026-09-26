package rigd

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/digitaldreamer3462/rigfile/internal/apply"
)

// Service names, one per OS convention.
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
type Unit struct {
	Path    string // where it is installed
	Content []byte // exactly the bytes to write
}

// UnitFor renders the service definition for the spec's OS.
func UnitFor(s ServiceSpec) (Unit, error) {
	if !absFor(s.GOOS, s.Exe) {
		return Unit{}, errors.New("rigd: the service needs the absolute path of the rigfile binary")
	}
	switch s.GOOS {
	case "darwin":
		return Unit{Path: filepath.Join(s.Home, "Library", "LaunchAgents", LaunchdLabel+".plist"), Content: []byte(plist(s))}, nil
	case "linux":
		return Unit{Path: filepath.Join(s.Home, ".config", "systemd", "user", SystemdUnit), Content: []byte(systemdUnit(s))}, nil
	case "windows":
		return Unit{Path: filepath.Join(s.StateDir, "rigd", "rigd-task.xml"), Content: taskXML(s)}, nil
	}
	return Unit{}, fmt.Errorf("rigd: no service manager known for %s", s.GOOS)
}

// absFor decides absoluteness by the target OS's rules, not the host's: the files are generated for s.GOOS.
func absFor(goos, p string) bool {
	if goos == "windows" {
		return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func plist(s ServiceSpec) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + LaunchdLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEsc(s.Exe) + `</string>
    <string>broker</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>` + xmlEsc(s.LogPath) + `</string>
  <key>StandardErrorPath</key>
  <string>` + xmlEsc(s.LogPath) + `</string>
</dict>
</plist>
`
}

// systemdQuote quotes one word for ExecStart: double quotes, with backslash, quote, and the % and $ specifiers escaped.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

func systemdUnit(s ServiceSpec) string {
	return `[Unit]
Description=Rigfile secret broker (rigd)
Documentation=https://github.com/digitaldreamer3462/rigfile/blob/main/docs/rigd.md

[Service]
Type=simple
ExecStart=` + systemdQuote(s.Exe) + ` broker run
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes
StandardOutput=append:` + strings.ReplaceAll(s.LogPath, " ", `\x20`) + `
StandardError=inherit

[Install]
WantedBy=default.target
`
}

// taskXML is a Windows Task Scheduler definition: runs at logon as the user, restarts on failure, never stops on battery,
// no time limit, hidden. It is UTF-16 with a byte-order mark, which is what schtasks itself writes.
func taskXML(s ServiceSpec) []byte {
	doc := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Rigfile secret broker (rigd)</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Hidden>true</Hidden>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>10</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEsc(s.Exe) + `</Command>
      <Arguments>broker run</Arguments>
    </Exec>
  </Actions>
</Task>
`
	u := utf16.Encode([]rune(strings.ReplaceAll(doc, "\n", "\r\n")))
	out := make([]byte, 0, 2+2*len(u))
	out = append(out, 0xFF, 0xFE)
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

// DecodeUTF16 turns the task file back into text (tests and `broker status`).
func DecodeUTF16(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}

// Activator runs the OS's service-manager commands. Tests replace it; the real one runs them.
type Activator interface {
	Run(argv []string) (output string, err error)
}

// Service installs, removes and drives the service.
type Service struct {
	Spec       ServiceSpec
	Act        Activator
	BackupRoot string // Rigfile's backup directory: the unit file is written through the journaled writer
}

// commands are the service-manager invocations per action. Errors of the "already" kind are tolerated by the caller.
func (s *Service) commands(action string, u Unit) [][]string {
	uid := "gui/" + s.Spec.UID
	switch s.Spec.GOOS {
	case "darwin":
		switch action {
		case "install":
			return [][]string{{"launchctl", "bootout", uid + "/" + LaunchdLabel}, {"launchctl", "bootstrap", uid, u.Path}}
		case "start":
			return [][]string{{"launchctl", "bootstrap", uid, u.Path}, {"launchctl", "kickstart", "-k", uid + "/" + LaunchdLabel}}
		case "stop", "uninstall":
			return [][]string{{"launchctl", "bootout", uid + "/" + LaunchdLabel}}
		}
	case "linux":
		switch action {
		case "install":
			return [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", SystemdUnit}}
		case "start":
			return [][]string{{"systemctl", "--user", "start", SystemdUnit}}
		case "stop":
			return [][]string{{"systemctl", "--user", "stop", SystemdUnit}}
		case "uninstall":
			return [][]string{{"systemctl", "--user", "disable", "--now", SystemdUnit}}
		}
	case "windows":
		switch action {
		case "install":
			return [][]string{{"schtasks", "/Create", "/TN", TaskName, "/XML", u.Path, "/F"}, {"schtasks", "/Run", "/TN", TaskName}}
		case "start":
			return [][]string{{"schtasks", "/Run", "/TN", TaskName}}
		case "stop":
			return [][]string{{"schtasks", "/End", "/TN", TaskName}}
		case "uninstall":
			return [][]string{{"schtasks", "/End", "/TN", TaskName}, {"schtasks", "/Delete", "/TN", TaskName, "/F"}}
		}
	}
	return nil
}

// tolerated reports output that only says the step had nothing to do.
func tolerated(out string) bool {
	o := strings.ToLower(out)
	for _, m := range []string{"no such process", "could not find service", "not loaded", "already", "no tasks are running", "does not exist", "cannot find the file", "not found", "input/output error"} {
		if strings.Contains(o, m) {
			return true
		}
	}
	return false
}

func (s *Service) run(action string, u Unit) error {
	cmds := s.commands(action, u)
	for i, argv := range cmds {
		out, err := s.Act.Run(argv)
		if err == nil {
			continue
		}
		msg := out + " " + err.Error()
		switch {
		case action == "stop" || action == "uninstall":
			continue // best effort: there may be nothing running
		case action == "install" && i < len(cmds)-1 && tolerated(msg):
			continue // clearing a previous registration
		case action == "start" && tolerated(msg):
			continue
		}
		return fmt.Errorf("%s: %v %s", strings.Join(argv, " "), err, strings.TrimSpace(out))
	}
	return nil
}

// Install writes the unit through the journaled writer, then activates it. If activation fails the write is rolled back
// so the machine is left as it was.
func (s *Service) Install() (string, error) {
	u, err := UnitFor(s.Spec)
	if err != nil {
		return "", err
	}
	w := &apply.Writer{BackupRoot: s.BackupRoot}
	_, err = w.WriteFileMode(u.Path, u.Content, 0o644)
	if err != nil {
		return "", err
	}
	runID, cerr := w.Commit("rigfile broker install")
	if cerr != nil {
		return "", cerr
	}
	if err := s.run("install", u); err != nil {
		if runID != "" {
			_, _ = apply.Rollback(s.BackupRoot, runID, false)
		}
		return "", fmt.Errorf("could not start the service (the file was rolled back): %w", err)
	}
	return u.Path, nil
}

// Uninstall stops and disables the service, then removes the unit file through the journaled writer.
func (s *Service) Uninstall() error {
	u, err := UnitFor(s.Spec)
	if err != nil {
		return err
	}
	_ = s.run("uninstall", u)
	w := &apply.Writer{BackupRoot: s.BackupRoot}
	if _, err := w.Delete(u.Path); err != nil {
		return err
	}
	_, err = w.Commit("rigfile broker uninstall")
	return err
}

// Installed reports whether the unit file is there.
func (s *Service) Installed() bool {
	u, err := UnitFor(s.Spec)
	if err != nil {
		return false
	}
	_, err = statFile(u.Path)
	return err == nil
}

// Start and Stop drive an installed service.
func (s *Service) Start() error { return s.drive("start") }
func (s *Service) Stop() error  { return s.drive("stop") }

func (s *Service) drive(action string) error {
	u, err := UnitFor(s.Spec)
	if err != nil {
		return err
	}
	if !s.Installed() {
		return errors.New("the service is not installed (rigfile broker install)")
	}
	return s.run(action, u)
}

func statFile(p string) (os.FileInfo, error) { return os.Stat(p) }
