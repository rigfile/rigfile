// Package svc generates and drives a per-user background service on each OS: a launchd agent (macOS), a `systemd --user`
// unit (Linux) or a scheduled task at logon (Windows). It is what runs `rigd` and the local model servers.
//
// Design (docs/rigd.md §5, docs/models.md §6): the definition is GENERATED as bytes and written through the journaled writer
// (backups, rollback); the OS's service-manager commands go through an injectable Activator, so tests never touch a real
// service manager and a failed activation rolls the file back. Nothing here reads the environment: the definitions are
// reproducible.
package svc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/rigfile/rigfile/internal/apply"
)

// Spec is everything a generated definition depends on.
type Spec struct {
	GOOS     string // darwin, linux, windows
	Home     string
	UID      string // numeric user id (launchd's gui/<uid> domain)
	StateDir string
	Name     string // "rigd", "model-local-coder": label com.rigfile.<Name>, unit rigfile-<Name>.service, task rigfile-<Name>

	Description string
	Docs        string // systemd Documentation= (optional)
	Exe         string // absolute path of the program
	Args        []string
	Env         map[string]string
	LogPath     string
	TaskFile    string // where the Windows task XML is kept (the file `schtasks /XML` reads)
}

// Unit is one generated service definition.
type Unit struct {
	Path    string // where it is installed
	Content []byte // exactly the bytes to write
}

// Label, UnitName and TaskName are the names the OS knows the service by.
func (s Spec) Label() string    { return "com.rigfile." + s.Name }
func (s Spec) UnitName() string { return "rigfile-" + s.Name + ".service" }
func (s Spec) TaskName() string { return "rigfile-" + s.Name }

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	envValRe = regexp.MustCompile(`^[A-Za-z0-9._:/@+=,-]{0,256}$`)
)

// absFor decides absoluteness by the target OS's rules, not the host's: the files are generated for s.GOOS.
func absFor(goos, p string) bool {
	if goos == "windows" {
		return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

func (s Spec) validate() error {
	switch {
	case !nameRe.MatchString(s.Name):
		return fmt.Errorf("svc: %q is not a service name", s.Name)
	case !absFor(s.GOOS, s.Exe):
		return errors.New("svc: the service needs the absolute path of the program")
	}
	for k, v := range s.Env {
		if !envKeyRe.MatchString(k) || !envValRe.MatchString(v) {
			return fmt.Errorf("svc: environment variable %q has a value that cannot be written safely", k)
		}
	}
	if s.GOOS == "windows" {
		for _, a := range append([]string{s.Exe}, s.Args...) {
			if strings.ContainsAny(a, "\"%^&|<>\r\n") {
				return fmt.Errorf("svc: %q contains a character that cannot be passed safely through the Windows task scheduler", a)
			}
		}
	}
	return nil
}

// UnitFor renders the service definition for the spec's OS.
func UnitFor(s Spec) (Unit, error) {
	if err := s.validate(); err != nil {
		return Unit{}, err
	}
	switch s.GOOS {
	case "darwin":
		return Unit{Path: filepath.Join(s.Home, "Library", "LaunchAgents", s.Label()+".plist"), Content: []byte(plist(s))}, nil
	case "linux":
		return Unit{Path: filepath.Join(s.Home, ".config", "systemd", "user", s.UnitName()), Content: []byte(systemdUnit(s))}, nil
	case "windows":
		p := s.TaskFile
		if p == "" {
			p = filepath.Join(s.StateDir, "svc", s.Name+"-task.xml")
		}
		return Unit{Path: p, Content: taskXML(s)}, nil
	}
	return Unit{}, fmt.Errorf("svc: no service manager known for %s", s.GOOS)
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func sortedEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func plist(s Spec) string {
	var args strings.Builder
	for _, a := range append([]string{s.Exe}, s.Args...) {
		args.WriteString("    <string>" + xmlEsc(a) + "</string>\n")
	}
	env := ""
	if len(s.Env) > 0 {
		env = "  <key>EnvironmentVariables</key>\n  <dict>\n"
		for _, k := range sortedEnv(s.Env) {
			env += "    <key>" + xmlEsc(k) + "</key>\n    <string>" + xmlEsc(s.Env[k]) + "</string>\n"
		}
		env += "  </dict>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + s.Label() + `</string>
  <key>ProgramArguments</key>
  <array>
` + args.String() + `  </array>
` + env + `  <key>RunAtLoad</key>
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

// systemdWord leaves a plain word bare and quotes anything else.
func systemdWord(a string) string {
	if a != "" && envValRe.MatchString(a) {
		return a
	}
	return systemdQuote(a)
}

func systemdUnit(s Spec) string {
	exec := systemdQuote(s.Exe)
	for _, a := range s.Args {
		exec += " " + systemdWord(a)
	}
	docs := ""
	if s.Docs != "" {
		docs = "Documentation=" + s.Docs + "\n"
	}
	env := ""
	for _, k := range sortedEnv(s.Env) {
		env += "Environment=" + systemdQuote(k+"="+s.Env[k]) + "\n"
	}
	return `[Unit]
Description=` + s.Description + `
` + docs + `
[Service]
Type=simple
ExecStart=` + exec + `
` + env + `Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes
StandardOutput=append:` + strings.ReplaceAll(s.LogPath, " ", `\x20`) + `
StandardError=inherit

[Install]
WantedBy=default.target
`
}

// winCommand returns the program and argument string of a Task Scheduler action. Environment variables need a `cmd`
// wrapper (the task has no environment of its own); every value that reaches it was validated to contain no shell
// metacharacter.
func winCommand(s Spec) (string, string) {
	q := func(a string) string {
		if a == "" || strings.ContainsAny(a, " \t") {
			return `"` + a + `"`
		}
		return a
	}
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = q(a)
	}
	if len(s.Env) == 0 {
		return s.Exe, strings.Join(args, " ")
	}
	var sets []string
	for _, k := range sortedEnv(s.Env) {
		sets = append(sets, `set "`+k+`=`+s.Env[k]+`"`)
	}
	inner := strings.Join(sets, "&& ") + `&& "` + s.Exe + `"`
	if len(args) > 0 {
		inner += " " + strings.Join(args, " ")
	}
	return "cmd.exe", `/d /s /c "` + inner + `"`
}

// taskXML is a Windows Task Scheduler definition: runs at logon as the user, restarts on failure, never stops on battery,
// no time limit, hidden. It is UTF-16 with a byte-order mark, which is what schtasks itself writes.
func taskXML(s Spec) []byte {
	cmd, args := winCommand(s)
	doc := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>` + xmlEsc(s.Description) + `</Description>
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
      <Command>` + xmlEsc(cmd) + `</Command>
      <Arguments>` + xmlEsc(args) + `</Arguments>
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

// DecodeUTF16 turns the task file back into text (tests and status output).
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

// Service installs, removes and drives one service.
type Service struct {
	Spec       Spec
	Act        Activator
	BackupRoot string // Rigfile's backup directory: the unit file is written through the journaled writer
	// Notes label the journal entries (shown by `rigfile rollback --list`); defaults name the service.
	InstallNote, UninstallNote string
}

// commands are the service-manager invocations per action. Errors of the "already" kind are tolerated by run.
func (s *Service) commands(action string, u Unit) [][]string {
	uid := "gui/" + s.Spec.UID
	label, unit, task := s.Spec.Label(), s.Spec.UnitName(), s.Spec.TaskName()
	switch s.Spec.GOOS {
	case "darwin":
		switch action {
		case "install":
			return [][]string{{"launchctl", "bootout", uid + "/" + label}, {"launchctl", "bootstrap", uid, u.Path}}
		case "start":
			return [][]string{{"launchctl", "bootstrap", uid, u.Path}, {"launchctl", "kickstart", "-k", uid + "/" + label}}
		case "stop", "uninstall":
			return [][]string{{"launchctl", "bootout", uid + "/" + label}}
		}
	case "linux":
		switch action {
		case "install":
			return [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", unit}}
		case "start":
			return [][]string{{"systemctl", "--user", "start", unit}}
		case "stop":
			return [][]string{{"systemctl", "--user", "stop", unit}}
		case "uninstall":
			return [][]string{{"systemctl", "--user", "disable", "--now", unit}}
		}
	case "windows":
		switch action {
		case "install":
			return [][]string{{"schtasks", "/Create", "/TN", task, "/XML", u.Path, "/F"}, {"schtasks", "/Run", "/TN", task}}
		case "start":
			return [][]string{{"schtasks", "/Run", "/TN", task}}
		case "stop":
			return [][]string{{"schtasks", "/End", "/TN", task}}
		case "uninstall":
			return [][]string{{"schtasks", "/End", "/TN", task}, {"schtasks", "/Delete", "/TN", task, "/F"}}
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
	if _, err = w.WriteFileMode(u.Path, u.Content, 0o644); err != nil {
		return "", err
	}
	runID, cerr := w.Commit(orDefault(s.InstallNote, "install service "+s.Spec.Name))
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
	_, err = w.Commit(orDefault(s.UninstallNote, "uninstall service "+s.Spec.Name))
	return err
}

// Installed reports whether the unit file is there.
func (s *Service) Installed() bool {
	u, err := UnitFor(s.Spec)
	if err != nil {
		return false
	}
	_, err = os.Stat(u.Path)
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
		return errors.New("the service is not installed")
	}
	return s.run(action, u)
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}
