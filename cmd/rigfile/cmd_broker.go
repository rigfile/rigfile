package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/rigd"
)

// rigdDir is where the broker keeps its token, addresses, config and audit log.
func rigdDir(e env) (string, error) {
	pi, err := platformInfo(e)
	if err != nil {
		return "", err
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		return "", err
	}
	return filepath.Join(sd, "rigd"), nil
}

const brokerUsage = `usage: rigfile broker <command>

  run                 run the broker in the foreground (Ctrl-C stops it)
  install | uninstall install or remove the per-user service (launchd, systemd --user, or a scheduled task)
  start | stop        start or stop it (the service if installed, otherwise a background process)
  status              show whether the broker is running
  enable | disable    turn Level 2 (surrogate secrets) on or off for rigfile exec
  exclude <server>    run one server at Level 1 (the real key in its environment)
  include <server>    undo exclude
`

func cmdBroker(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.err, brokerUsage)
		return 2
	}
	dir, err := rigdDir(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	switch args[0] {
	case "run":
		return brokerRun(args[1:], e, dir)
	case "status":
		return brokerStatus(e, dir)
	case "install", "uninstall", "start", "stop":
		return brokerService(args[0], e, dir)
	case "enable", "disable":
		cfg, err := rigd.LoadConfig(dir)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		cfg.Enabled = args[0] == "enable"
		if err := cfg.Save(dir); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		if cfg.Enabled {
			fmt.Fprintln(e.out, "Level 2 is on. `rigfile exec` will use the broker when it is running (rigfile broker run).")
		} else {
			fmt.Fprintln(e.out, "Level 2 is off. Servers run at Level 1.")
		}
		return 0
	case "exclude", "include":
		if len(args) != 2 {
			fmt.Fprint(e.err, brokerUsage)
			return 2
		}
		cfg, err := rigd.LoadConfig(dir)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		cfg.Exclude(args[1], args[0] == "exclude")
		if err := cfg.Save(dir); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		if args[0] == "exclude" {
			fmt.Fprintf(e.out, "%s will run at Level 1: its real key is in its process. Use this only for tools that ignore proxy and CA settings.\n", args[1])
		} else {
			fmt.Fprintf(e.out, "%s is back at Level 2.\n", args[1])
		}
		return 0
	}
	fmt.Fprint(e.err, brokerUsage)
	return 2
}

func brokerRun(args []string, e env, dir string) int {
	fs := flag.NewFlagSet("broker run", flag.ContinueOnError)
	fs.SetOutput(e.err)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	st, err := openStore(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	b := &rigd.Broker{Dir: dir, Version: version, Resolve: func(ref string) ([]byte, error) { return st.Get(ref) }}
	if err := b.Start(); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	info := b.Info()
	fmt.Fprintf(e.out, "rigd is running (pid %d, proxy %s). Audit log: %s\nPress Ctrl-C to stop.\n", info.PID, info.Proxy, filepath.Join(dir, rigd.AuditFile))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	_ = b.Close()
	fmt.Fprintln(e.out, "rigd stopped.")
	return 0
}

func brokerStatus(e env, dir string) int {
	cfg, _ := rigd.LoadConfig(dir)
	state := "off"
	if cfg.Enabled {
		state = "on"
	}
	fmt.Fprintf(e.out, "Level 2: %s\n", state)
	for _, s := range cfg.Excluded {
		fmt.Fprintf(e.out, "  excluded: %s (Level 1)\n", s)
	}
	c, err := rigd.ClientFromDir(dir)
	if err == nil {
		var rep *rigd.StatusReply
		if rep, err = c.Status(); err == nil {
			fmt.Fprintf(e.out, "broker: running (pid %d, %d live session(s), version %s)\n", rep.PID, rep.Sessions, rep.Version)
			return 0
		}
	}
	if errors.Is(err, rigd.ErrNotRunning) {
		fmt.Fprintln(e.out, "broker: not running (rigfile broker run)")
		return 1
	}
	fmt.Fprintln(e.err, "rigfile:", err)
	return 1
}

func goosFor(pi *platform.Info) string {
	if pi.OS == platform.MacOS {
		return "darwin"
	}
	return string(pi.OS)
}

type execActivator struct{}

func (execActivator) Run(argv []string) (string, error) {
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

func brokerService(action string, e env, dir string) int {
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	sd, err := stateDirFor(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	home, err := pi.Home()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	exe := e.exe
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
	}
	act := e.brokerAct
	if act == nil {
		act = execActivator{}
	}
	svc := &rigd.Service{
		Spec: rigd.ServiceSpec{GOOS: goosFor(pi), Home: home, Exe: exe, UID: strconv.Itoa(os.Getuid()), StateDir: sd, LogPath: filepath.Join(dir, "rigd.log")},
		Act:  act, BackupRoot: filepath.Join(sd, "backups"),
	}
	fail := func(err error) int { fmt.Fprintln(e.err, "rigfile:", err); return 1 }
	switch action {
	case "install":
		path, err := svc.Install()
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(e.out, "Installed the broker service (%s) and started it. Undo with `rigfile broker uninstall`; `rigfile rollback` also restores the file.\n", path)
		fmt.Fprintln(e.out, "The service reads secrets from your OS keychain. If you use the encrypted-file backend, it cannot prompt for a passphrase: run `rigfile broker run` yourself instead.")
		return 0
	case "uninstall":
		if err := svc.Uninstall(); err != nil {
			return fail(err)
		}
		fmt.Fprintln(e.out, "Removed the broker service.")
		return 0
	case "start":
		if svc.Installed() {
			if err := svc.Start(); err != nil {
				return fail(err)
			}
			fmt.Fprintln(e.out, "Started the broker service.")
			return 0
		}
		return brokerBackground(e, dir, exe)
	case "stop":
		if svc.Installed() {
			if err := svc.Stop(); err != nil {
				return fail(err)
			}
			fmt.Fprintln(e.out, "Stopped the broker service.")
			return 0
		}
		return brokerTerminate(e, dir)
	}
	return 2
}

// brokerBackground starts `rigfile broker run` detached from this terminal (no service manager involved).
func brokerBackground(e env, dir, exe string) int {
	if c, err := rigd.ClientFromDir(dir); err == nil {
		if _, err := c.Status(); err == nil {
			fmt.Fprintln(e.out, "The broker is already running.")
			return 0
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	logPath := filepath.Join(dir, "rigd.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	defer lf.Close()
	cmd := exec.Command(exe, "broker", "run")
	cmd.Stdout, cmd.Stderr = lf, lf
	platform.Detach(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	_ = cmd.Process.Release()
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		if c, err := rigd.ClientFromDir(dir); err == nil {
			if _, err := c.Status(); err == nil {
				fmt.Fprintf(e.out, "The broker is running in the background (log: %s).\n", logPath)
				return 0
			}
		}
	}
	fmt.Fprintf(e.err, "rigfile: the broker did not come up; see %s\n", logPath)
	return 1
}

// brokerTerminate stops a background broker by its recorded pid.
func brokerTerminate(e env, dir string) int {
	c, err := rigd.ClientFromDir(dir)
	if err != nil {
		fmt.Fprintln(e.out, "The broker is not running.")
		return 0
	}
	st, err := c.Status()
	if err != nil {
		fmt.Fprintln(e.out, "The broker is not running.")
		return 0
	}
	if err := platform.Terminate(st.PID); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintln(e.out, "Stopped the broker.")
	return 0
}
