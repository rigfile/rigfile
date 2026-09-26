package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/digitaldreamer3462/rigfile/internal/rigd"
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
