package login

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/rigfile/rigfile/internal/platform"
	"github.com/rigfile/rigfile/internal/secrets"
)

// Method is how a provider is signed in.
type Method string

const (
	APIKey    Method = "api-key"
	VendorCLI Method = "vendor-cli"
	OAuthWeb  Method = "oauth"
)

// Provider says how one named provider signs in. Only what is documented by the vendor is listed; every other
// provider falls back to an API key, which is always possible.
type Provider struct {
	Default Method
	// Manual: instructions for a sign-in Rigfile cannot start for the user (asks "done?" afterwards).
	Manual string
	// Login and Check are the vendor's own commands; Check is optional (exit 0 = signed in).
	Login, Check []string
	// OAuth, when set, enables the web and device flows for this provider.
	OAuth *OAuth
}

// Providers is the built-in table. UNVERIFIED items are listed in docs/owner-checklist.md.
var Providers = map[string]Provider{
	"claude-code": {Default: VendorCLI, Manual: "run `claude`, then type /login (or configure an API key or gateway)"},
	"codex":       {Default: VendorCLI, Login: []string{"codex", "login"}}, // documented: docs/targets/codex.md §10
	"github":      {Default: VendorCLI, Login: []string{"gh", "auth", "login"}, Check: []string{"gh", "auth", "status"}},
	"gemini-cli":  {Default: VendorCLI, Manual: "run `gemini` once and choose how to sign in (Rigfile cannot start it for you)"},
}

// Result is the outcome for one login.
type Result struct {
	Provider string
	Method   Method
	OK       bool
	Note     string
}

// Env is everything the runner needs from the machine.
type Env struct {
	In       io.Reader
	Out      io.Writer
	Getenv   func(string) string
	Plat     *platform.Info
	Store    secrets.Store
	Hidden   func(prompt string) (string, error)            // hidden prompt (API keys)
	Line     func(prompt string) (string, error)            // a visible line (confirmations)
	RunCmd   func(ctx context.Context, argv []string) error // runs a vendor command with the terminal attached
	Client   *Client
	LookPath func(string) (string, error)
}

// Headless reports whether this session has no browser to open: over SSH, or a Linux session without a display.
func Headless(pi *platform.Info, getenv func(string) string) bool {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return true
	}
	if pi != nil && pi.OS == platform.Linux && !pi.WSL && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return true
	}
	return false
}

// Request is one login to perform.
type Request struct {
	Provider string
	Method   Method // "" = the provider's default
	Reason   string
}

// RunAll walks the requests one by one: it never stops at a failure (the rest still get their turn), and it
// reports each outcome without ever printing a credential.
func RunAll(ctx context.Context, env Env, reqs []Request) []Result {
	var out []Result
	for i, rq := range reqs {
		fmt.Fprintf(env.Out, "\n[%d/%d] %s", i+1, len(reqs), rq.Provider)
		if rq.Reason != "" {
			fmt.Fprintf(env.Out, "  (%s)", rq.Reason)
		}
		fmt.Fprintln(env.Out)
		out = append(out, runOne(ctx, env, rq))
	}
	return out
}

func runOne(ctx context.Context, env Env, rq Request) Result {
	p, known := Providers[rq.Provider]
	m := rq.Method
	if m == "" {
		m = p.Default
		if !known || m == "" {
			m = APIKey
		}
	}
	res := Result{Provider: rq.Provider, Method: m}
	fail := func(format string, a ...any) Result {
		res.Note = fmt.Sprintf(format, a...)
		fmt.Fprintln(env.Out, "  x", res.Note)
		return res
	}
	ok := func(format string, a ...any) Result {
		res.OK, res.Note = true, fmt.Sprintf(format, a...)
		fmt.Fprintln(env.Out, "  ok", res.Note)
		return res
	}
	switch m {
	case APIKey:
		if env.Store == nil || env.Hidden == nil {
			return fail("cannot ask for an API key here (no secret store or terminal)")
		}
		ref := "logins/" + rq.Provider + "/api_key"
		if _, err := env.Store.Get(ref); err == nil {
			return ok("an API key is already stored (%s)", ref)
		}
		v, err := env.Hidden("  API key for " + rq.Provider + " (input is hidden; empty skips): ")
		if err != nil {
			return fail("could not read the key: %v", err)
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return fail("skipped")
		}
		if err := env.Store.Set(ref, []byte(v)); err != nil {
			return fail("could not store it: %v", err)
		}
		return ok("stored in the secret store as %s", ref)
	case VendorCLI:
		if len(p.Login) == 0 {
			if p.Manual == "" {
				return fail("no login command is known for %q; use method api-key or sign in with the vendor's tool", rq.Provider)
			}
			fmt.Fprintf(env.Out, "  Rigfile cannot start this one for you: %s\n", p.Manual)
			if env.Line == nil {
				return fail("do this yourself, then re-run")
			}
			ans, _ := env.Line("  Done? [y/N] ")
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "y") {
				return ok("you said it is done")
			}
			return fail("not done")
		}
		if env.LookPath != nil {
			if _, err := env.LookPath(p.Login[0]); err != nil {
				return fail("%s is not installed (it is the vendor's own login command)", p.Login[0])
			}
		}
		if env.RunCmd == nil {
			return fail("cannot run %s here", strings.Join(p.Login, " "))
		}
		fmt.Fprintf(env.Out, "  running the vendor's own command: %s\n", strings.Join(p.Login, " "))
		if err := env.RunCmd(ctx, p.Login); err != nil {
			return fail("%s failed: %v", p.Login[0], err)
		}
		if len(p.Check) > 0 {
			if err := env.RunCmd(ctx, p.Check); err != nil {
				return fail("the login command finished but `%s` says you are not signed in", strings.Join(p.Check, " "))
			}
		}
		return ok("signed in through %s", p.Login[0])
	case OAuthWeb:
		if p.OAuth == nil {
			return fail("no OAuth client is registered for %q; use method api-key or vendor-cli", rq.Provider)
		}
		cl := env.Client
		if cl == nil {
			cl = &Client{}
		}
		var tok *Token
		var err error
		if Headless(env.Plat, env.Getenv) && p.OAuth.DeviceURL != "" {
			fmt.Fprintln(env.Out, "  no browser here: using the device-code sign-in")
			tok, err = cl.DeviceFlow(ctx, *p.OAuth)
		} else {
			tok, err = cl.LoopbackPKCE(ctx, *p.OAuth)
		}
		if err != nil {
			return fail("%v", err)
		}
		if env.Store == nil {
			return fail("no secret store to keep the token in")
		}
		if err := env.Store.Set("logins/"+rq.Provider+"/access_token", []byte(tok.Access)); err != nil {
			return fail("could not store the token: %v", err)
		}
		if tok.Refresh != "" {
			if err := env.Store.Set("logins/"+rq.Provider+"/refresh_token", []byte(tok.Refresh)); err != nil {
				return fail("could not store the refresh token: %v", err)
			}
		}
		return ok("token stored in the secret store as logins/%s/access_token", rq.Provider)
	}
	return fail("unknown login method %q", m)
}

// SystemRun runs a vendor command attached to the user's terminal.
func SystemRun(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return errors.New("empty command")
	}
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
