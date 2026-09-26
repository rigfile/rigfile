// Command release builds a Rigfile release from source: cross-compiled binaries, deterministic archives, a SHA256SUMS
// list (signed with minisign when a key is given), and every package-manager manifest rendered from those checksums:
// Homebrew, Scoop, winget, a .deb, npm and pip wrappers. It is code rather than CI YAML so it can be run and tested
// on a laptop. Publishing (uploading, pushing to a tap, submitting to winget) is deliberately not here: that needs
// the owner's accounts (docs/stage-4-owner-checks.md).
//
//	go run ./tools/release -version 1.0.0 -out dist -pubkey <minisign public key> [-sign-key key.sec]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target is one build.
type Target struct{ OS, Arch string }

func (t Target) String() string { return t.OS + "/" + t.Arch }

var allTargets = []Target{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}}

// Config is one release run.
type Config struct {
	Version string // without the leading v
	Out     string
	Targets []Target
	PubKey  string // minisign public key (base64 line) compiled into the binary for self-update
	Package string // main package to build
	Module  string
	Repo    string // owner/repo of the GitHub releases
	Epoch   int64  // modification time inside archives (reproducible)
	Scripts string // directory holding install.sh and install.ps1 templates
}

func main() {
	var (
		version = flag.String("version", "", "release version, e.g. 1.0.0 (required)")
		out     = flag.String("out", "dist", "output directory (created; must be empty or absent)")
		targets = flag.String("targets", "", "comma-separated os/arch list (default: all six)")
		pubkey  = flag.String("pubkey", "", "minisign public key (file or base64 line) built into the binary")
		signKey = flag.String("sign-key", "", "minisign secret key file: sign SHA256SUMS with the minisign tool")
		repo    = flag.String("repo", "digitaldreamer3462/rigfile", "GitHub owner/repo the release is published to")
	)
	flag.Parse()
	if *version == "" {
		fmt.Fprintln(os.Stderr, "release: -version is required")
		os.Exit(2)
	}
	cfg := Config{Version: strings.TrimPrefix(*version, "v"), Out: *out, Package: "./cmd/rigfile", Repo: *repo,
		Module: "github.com/digitaldreamer3462/rigfile", Epoch: 1700000000, Scripts: "scripts"}
	if *targets == "" {
		cfg.Targets = allTargets
	} else {
		for _, t := range strings.Split(*targets, ",") {
			o, a, ok := strings.Cut(strings.TrimSpace(t), "/")
			if !ok {
				fmt.Fprintf(os.Stderr, "release: bad target %q (want os/arch)\n", t)
				os.Exit(2)
			}
			cfg.Targets = append(cfg.Targets, Target{o, a})
		}
	}
	if *pubkey != "" {
		k := *pubkey
		if b, err := os.ReadFile(*pubkey); err == nil {
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			k = strings.TrimSpace(lines[len(lines)-1])
		}
		cfg.PubKey = k
	}
	if err := Run(cfg, *signKey); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

// Run performs the whole release into cfg.Out.
func Run(cfg Config, signKey string) error {
	if ents, err := os.ReadDir(cfg.Out); err == nil && len(ents) > 0 {
		return fmt.Errorf("%s is not empty", cfg.Out)
	}
	if err := os.MkdirAll(cfg.Out, 0o755); err != nil {
		return err
	}
	var built []Artifact
	for _, t := range cfg.Targets {
		a, err := buildArchive(cfg, t)
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		built = append(built, a)
		fmt.Printf("built %s\n", filepath.Base(a.Path))
	}
	for _, t := range cfg.Targets {
		if t.OS != "linux" {
			continue
		}
		a, err := buildDeb(cfg, t)
		if err != nil {
			return fmt.Errorf("deb %s: %w", t, err)
		}
		built = append(built, a)
		fmt.Printf("built %s\n", filepath.Base(a.Path))
	}
	if err := writeChecksums(cfg, built); err != nil {
		return err
	}
	if signKey != "" {
		sums := filepath.Join(cfg.Out, "SHA256SUMS")
		c := exec.Command("minisign", "-S", "-s", signKey, "-m", sums, "-x", sums+".minisig", "-t", "rigfile v"+cfg.Version)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("minisign: %w (is the minisign tool installed?)", err)
		}
	}
	if err := renderPackaging(cfg, built); err != nil {
		return err
	}
	if err := renderInstallers(cfg); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(cfg.Out, ".build"))
	fmt.Printf("release v%s written to %s\n", cfg.Version, cfg.Out)
	return nil
}

// renderInstallers writes install.sh and install.ps1 next to the archives with the release key and repository filled
// in. They are release assets rather than part of SHA256SUMS: the key inside them is what anchors verification.
func renderInstallers(cfg Config) error {
	for _, name := range []string{"install.sh", "install.ps1"} {
		b, err := os.ReadFile(filepath.Join(cfg.Scripts, name))
		if err != nil {
			return err
		}
		s := strings.ReplaceAll(string(b), "__RIGFILE_REPO__", cfg.Repo)
		if cfg.PubKey != "" {
			s = strings.ReplaceAll(s, "__RIGFILE_PUBKEY__", cfg.PubKey)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(cfg.Out, name), []byte(s), mode); err != nil {
			return err
		}
	}
	return nil
}
