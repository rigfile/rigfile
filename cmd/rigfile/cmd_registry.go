package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rigfile/rigfile/internal/login"
	"github.com/rigfile/rigfile/internal/regclient"
	"github.com/rigfile/rigfile/internal/sigverify"
	"github.com/rigfile/rigfile/internal/source"
)

var nonRef = regexp.MustCompile(`[^a-z0-9_-]+`)

// tokenRef is where the sign-in token for a registry lives in the secret store.
func tokenRef(base string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	return "registry/" + nonRef.ReplaceAllString(strings.ToLower(host), "_") + "/token"
}

// registryBase resolves the registry origin from --registry or $RIGFILE_REGISTRY.
func registryBase(e env, flagVal string) (string, error) {
	v := flagVal
	if v == "" {
		v = e.getenv("RIGFILE_REGISTRY")
	}
	if v == "" {
		return "", errors.New("no registry is configured: pass --registry https://registry.example.org or set RIGFILE_REGISTRY")
	}
	return regclient.ValidateBase(v)
}

// registryBaseQuiet is registryBase for the places where having no registry is normal (plan/apply).
func registryBaseQuiet(e env, flagVal string) string {
	b, err := registryBase(e, flagVal)
	if err != nil {
		return ""
	}
	return b
}

// storedToken reads the saved token for a registry without ever prompting or printing (anonymous when unavailable).
func storedToken(e env, base string) string {
	q := e
	q.err = io.Discard
	q.in = strings.NewReader("")
	pi, err := platformInfo(e)
	if err != nil {
		return ""
	}
	st, err := openStore(q, pi)
	if err != nil {
		return ""
	}
	v, err := st.Get(tokenRef(base))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(v))
}

func regToken(e env) func(string) string {
	return func(base string) string { return storedToken(e, base) }
}

func regClient(e env, base string) *regclient.Client {
	return &regclient.Client{Base: base, Token: func() string { return storedToken(e, base) }}
}

func cmdLogin(args []string, e env) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(e.err)
	reg := fs.String("registry", "", "registry address (default $RIGFILE_REGISTRY)")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 0 {
		fmt.Fprintln(e.err, "usage: rigfile login [--registry https://registry.example.org]")
		return 2
	}
	base, err := registryBase(e, *reg)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	store, err := openStore(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	cl := &login.Client{Show: func(f string, a ...any) { fmt.Fprintf(e.out, f, a...) }}
	if e.sleep != nil {
		cl.Sleep = e.sleep
	}
	tok, err := cl.DeviceFlow(context.Background(), login.OAuth{
		DeviceURL: base + "/v1/device/code", TokenURL: base + "/v1/device/token", ClientID: "rigfile-cli",
	})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if err := store.Set(tokenRef(base), []byte(tok.Access)); err != nil {
		fmt.Fprintln(e.err, "rigfile: could not store the token:", err)
		return 1
	}
	who, err := regClient(e, base).Me(context.Background())
	if err != nil {
		fmt.Fprintln(e.err, "rigfile: signed in, but could not confirm the account:", err)
		return 1
	}
	fmt.Fprintf(e.out, "signed in to %s as %s (token stored in the %s)\n", base, who, store.Kind())
	return 0
}

func cmdLogout(args []string, e env) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(e.err)
	reg := fs.String("registry", "", "registry address (default $RIGFILE_REGISTRY)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	base, err := registryBase(e, *reg)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	pi, err := platformInfo(e)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	store, err := openStore(e, pi)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if err := regClient(e, base).Revoke(context.Background()); err != nil {
		fmt.Fprintln(e.err, "warning: could not revoke the token on the server:", err)
	}
	if err := store.Delete(tokenRef(base)); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintln(e.out, "signed out of", base)
	return 0
}

func cmdWhoami(args []string, e env) int {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	fs.SetOutput(e.err)
	reg := fs.String("registry", "", "registry address (default $RIGFILE_REGISTRY)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	base, err := registryBase(e, *reg)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	who, err := regClient(e, base).Me(context.Background())
	if err != nil {
		fmt.Fprintln(e.err, "rigfile: not signed in to", base+":", err)
		return 1
	}
	fmt.Fprintf(e.out, "%s (%s)\n", who, base)
	return 0
}

// pollPublished waits for the registry's scan of a just-uploaded version and prints the outcome. It returns the exit
// code: 0 published, 1 rejected or timed out.
func pollPublished(e env, c *regclient.Client, owner, name, version string) int {
	every := e.pollEvery
	if every == 0 {
		every = 2 * time.Second
	}
	deadline := time.Now().Add(3 * time.Minute)
	fmt.Fprintf(e.out, "uploaded %s/%s@%s; the registry is scanning it...\n", owner, name, version)
	for time.Now().Before(deadline) {
		v, err := c.VersionInfo(context.Background(), owner, name, version)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		switch v.Status {
		case "published":
			fmt.Fprintf(e.out, "published %s/%s@%s\n", owner, name, version)
			for _, w := range v.Warnings {
				fmt.Fprintf(e.out, "  warning: %s\n", w.Message)
			}
			return 0
		case "rejected":
			fmt.Fprintf(e.err, "rigfile: the registry rejected %s/%s@%s:\n", owner, name, version)
			for _, f := range v.Findings {
				loc := f.File
				if f.Line > 0 {
					loc = fmt.Sprintf("%s:%d", f.File, f.Line)
				}
				fmt.Fprintf(e.err, "  - %s %s %s %s\n", f.Kind, f.Rule, loc, f.Message)
			}
			fmt.Fprintln(e.err, "  fix the problems and publish a NEW version number (versions are immutable)")
			return 1
		}
		time.Sleep(every)
	}
	fmt.Fprintln(e.err, "rigfile: the scan is taking longer than expected; check later with: rigfile pull", owner+"/"+name+"@"+version)
	return 1
}

// registryFetcher is the fetcher for registry sources: it sends the stored token and verifies each version's Sigstore
// signature on THIS machine, whatever the registry claims.
func registryFetcher(e env) *source.RegistryFetcher {
	return &source.RegistryFetcher{Token: regToken(e), Verify: func(ctx context.Context, spec source.Spec, version string, tarball []byte) (*source.SignerInfo, error) {
		owner, name, _ := strings.Cut(spec.Path, "/")
		bundle, err := regClient(e, spec.URL).Bundle(ctx, owner, name, version)
		if err != nil {
			return &source.SignerInfo{Err: "could not fetch the signature: " + err.Error()}, nil
		}
		if bundle == nil {
			return nil, nil // unsigned
		}
		verify := e.verifySig
		if verify == nil {
			pi, err := platformInfo(e)
			if err != nil {
				return &source.SignerInfo{Err: err.Error()}, nil
			}
			sd, err := stateDirFor(e, pi)
			if err != nil {
				return &source.SignerInfo{Err: err.Error()}, nil
			}
			tm, err := sigverify.TrustedRoot("", sd)
			if err != nil {
				return &source.SignerInfo{Err: err.Error()}, nil
			}
			verify = func(b, t []byte) (*sigverify.Result, error) { return sigverify.Verify(tm, b, t) }
		}
		res, err := verify(bundle, tarball)
		if err != nil {
			return &source.SignerInfo{Err: err.Error()}, nil
		}
		return &source.SignerInfo{Subject: res.Subject, Issuer: res.Issuer, BundleSHA256: res.BundleSHA256, ByPublisher: sigverify.PublisherIdentity(res.Issuer, res.Subject, owner)}, nil
	}}
}
