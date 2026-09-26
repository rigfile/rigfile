// Command rigfile-registry runs the Rigfile registry service and its operator tools (docs/registry.md §8).
//
//	rigfile-registry serve             # HTTP API + web pages + scan workers
//	rigfile-registry migrate           # apply database migrations and exit
//	rigfile-registry admin <command>   # moderation and break-glass tools (see `admin help`)
//
// Configuration is environment variables (RIGFILE_REGISTRY_*), never flags, so secrets stay out of process listings.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/pkgcheck"
	"github.com/digitaldreamer3462/rigfile/internal/registry"
	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
	"github.com/digitaldreamer3462/rigfile/internal/scan"
	"github.com/digitaldreamer3462/rigfile/internal/source"
)

type env struct {
	getenv func(string) string
	out    io.Writer
	err    io.Writer
}

func main() {
	e := env{getenv: os.Getenv, out: os.Stdout, err: os.Stderr}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], e))
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: rigfile-registry <command>

  serve                 run the registry (API, web pages, scan workers)
  migrate               apply database migrations and exit
  admin <command>       operator tools:
      create-user --login L --github-id N [--admin]   create an account without GitHub (bootstrap, deployment tests)
      token --login L [--name N] [--days D]           mint an API token for an account (printed once)
      reports                                         list open abuse reports
      resolve-report --id N --status actioned|dismissed
      takedown --rig owner/name [--version V] --reason R   remove a version (or the whole rig)
      disable-user --login L | enable-user --login L
      verify-publisher --login L --kind person|organisation|domain [--reason NOTE] | unverify-publisher --login L
      held                                            list versions held for review
      release --id N [--reason R] | reject --id N --reason R
      approve-public --rig owner/name                 let a rig that needed review go public
      publishing pause --reason R | publishing resume incident switch: refuse uploads
      revoke-tokens --login L | revoke-tokens --all   revoke API tokens
      audit [--limit N]                               show the newest audit entries

configuration: RIGFILE_REGISTRY_PUBLIC_URL, _DATABASE_URL, _BLOB (fs:/path | s3), _GITHUB_CLIENT_ID, _GITHUB_CLIENT_SECRET[_FILE],
_ADMINS, _LISTEN, _TRUST_PROXY, _MAX_UPLOAD_MB (see docs/registry.md §8)
`)
}

func run(ctx context.Context, args []string, e env) int {
	if len(args) == 0 {
		usage(e.err)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(ctx, e)
	case "migrate":
		cfg, err := loadConfig(e, false)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile-registry:", err)
			return 1
		}
		db, err := registry.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile-registry:", err)
			return 1
		}
		db.Close()
		fmt.Fprintln(e.out, "database is up to date")
		return 0
	case "admin":
		return admin(ctx, args[1:], e)
	case "help", "-h", "--help":
		usage(e.out)
		return 0
	}
	usage(e.err)
	return 2
}

// loadConfig reads the environment. Admin tools do not need the GitHub client, so they skip that part of validation.
func loadConfig(e env, full bool) (registry.Config, error) {
	get := e.getenv
	if !full {
		g := get
		get = func(k string) string {
			if v := g(k); v != "" {
				return v
			}
			switch k {
			case "RIGFILE_REGISTRY_PUBLIC_URL":
				return "http://localhost"
			case "RIGFILE_REGISTRY_BLOB":
				return "fs:/tmp/none"
			case "RIGFILE_REGISTRY_GITHUB_CLIENT_ID", "RIGFILE_REGISTRY_GITHUB_CLIENT_SECRET":
				return "unused"
			}
			return ""
		}
	}
	return registry.ConfigFromEnv(get, os.ReadFile)
}

func openBlobs(cfg registry.Config) (blob.Store, error) {
	if cfg.Blob == "s3" {
		return blob.OpenS3(cfg.S3)
	}
	return blob.Open(cfg.Blob)
}

func serve(ctx context.Context, e env) int {
	cfg, err := loadConfig(e, true)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile-registry:", err)
		return 1
	}
	log := slog.New(slog.NewJSONHandler(e.out, nil))
	db, err := registry.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile-registry:", err)
		return 1
	}
	defer db.Close()
	blobs, err := openBlobs(cfg)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile-registry:", err)
		return 1
	}
	store := registry.NewStore(db)
	gh := &registry.GitHubHTTP{ClientID: cfg.GitHubID, ClientSecret: cfg.GitHubSecret, WebBase: cfg.GitHubWeb, APIBase: cfg.GitHubAPI}
	srv := registry.NewServer(cfg, store, blobs, gh, log)

	sc := &registry.Scanner{Store: store, Blobs: blobs, Log: log, Limits: source.DefaultLimits, PopularStars: cfg.PopularStars, Scan: func() (*scan.Scanner, error) { return scan.New(scan.Options{}) }}
	if !cfg.OSVOff {
		sc.Packages = &pkgcheck.Client{BaseURL: cfg.OSVURL}
	}
	go sc.Run(ctx, cfg.ScanWorkers)

	hs := &http.Server{
		Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("registry listening", "addr", cfg.Listen, "public_url", cfg.PublicURL)
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
		return 0
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(e.err, "rigfile-registry:", err)
			return 1
		}
	}
	return 0
}
