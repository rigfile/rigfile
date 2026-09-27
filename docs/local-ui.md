# The local checklist page: `rigfile ui` (S8-M5)

`rigfile ui [<rig-dir>]` opens a page in your browser, served from your own computer, that shows what applying the rig would change and what it still needs from you. It is for people who do not live in a terminal; everything it does the command line does too, through the same code.

## What it shows and does

1. **What will change:** the same information `rigfile plan` prints (files, MCP servers, hooks, permissions, tools, warnings), sectioned per target with colour-coded symbols (green `+` new, blue `~` changed, amber `!` refused conflict, red `-` removed, grey `=` unchanged) instead of one flat text block. Per-OS items that do not apply to this machine are collapsed behind a "Not applicable here (N items)" disclosure, closed by default — everything else stays visible, since this is the review screen.
2. **What you need to provide:** a checklist. For each secret the rig declares: whether it is already stored, why it is needed, where to get it, and a password field to store it in your operating system's secret store (or the encrypted file). For each sign-in: what it is for, and the command that walks through it (`rigfile logins`; sign-ins are vendor flows that stay in the terminal).
3. **Apply:** a checkbox ("I have read the plan") and a button. It runs `rigfile apply --yes` for the rig: every file is backed up first, `rigfile rollback` undoes it, and a rig whose lockfile no longer matches is refused exactly as on the command line. An item that conflicts with something the rig does not own (exit code 3: "applied but some items were refused") is a normal, protective outcome, not a failure — the page still says "Done", with the refusal itself named in the transcript. Only a real error (any other non-zero exit code) shows "That did not work".

Flags are the same as `plan` and `apply` (`--layers`, `--registry`, `--target`, `--no-git` ...), plus `--no-open` to print the address instead of opening a browser. The page stops with the Stop button, Ctrl-C, or after 30 minutes without a request.

## How it is guarded

The page can store secrets and change files, so it is treated like the broker's API:

| Risk | Handling |
|---|---|
| Another user or machine reaches it | listens on `127.0.0.1` only, on a random port |
| A web page you visit talks to it (DNS rebinding) | the `Host` header must be the listener's own address |
| A web page you visit posts to it (CSRF) | a browser `Origin` must be the page's own, **or the literal string `null`** (found live, 2026-09-27: real Chrome sends exactly that, not a real origin, on the plain `<form method="post">` navigations this page uses, because of its own `Referrer-Policy: no-referrer` below — expected, spec-correct, not forgeable); `Sec-Fetch-Site` must be same-origin or none (unspoofable by page script, so it carries the real weight when Origin is `null`); every POST also needs a CSRF token that only the page carries |
| Someone else on the machine guesses the address | the address carries a random one-time token, swapped on first use for an `HttpOnly`, `SameSite=Strict` cookie and removed from the address bar (no token in history, no referrer: `Referrer-Policy: no-referrer`) |
| Script injection from a rig's text | plan/result text is escaped by `internal/localui/render.go` before any of its own (trusted, constant) HTML is added around it, whichever of its rendering branches a line falls into; the page has no script and no inline style; `Content-Security-Policy: default-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'` |
| Arbitrary secrets written through the page | only refs the rig declares can be set |
| A secret leaking back | the value is read from the POST body, stored, and never rendered, logged or put in an error (errors are scrubbed of it); bodies are capped at 16 KiB |
| Left running | idle timeout of 30 minutes |

What it does not do: it is not a defence against another program running as you (which can read the secret store or the cookie jar anyway), and it does not run vendor sign-ins.

## Tests

`internal/localui`: `TestLandingSwapsTheTokenForACookieAndEscapesEverything`, `TestGuardRefusesRebindingCrossOriginAndMissingCSRF`, `TestApplySucceedsWithARealBrowsersNullOrigin`, `TestStoringSecretsAndApplying`, `TestQuitAndIdleTimeout`, `TestParseOpRow`, `TestTranscriptEscapesEverything`, `TestTranscriptSectionsBySymbolAndCollapsesNotApplicable`, `TestTranscriptNeverProducesScriptOrInlineStyle` (`render.go`: the plain-text transcript to sectioned-HTML transform, and its one invariant — every raw-text branch is escaped, whichever one a line falls into). `cmd/rigfile`: `TestUIShowsThePlanStoresDeclaredSecretsAndApplies` (the whole command: plan, declared and undeclared secrets, apply, quit; nothing written by looking), `TestUIApplyWithRefusedConflictsIsShownAsSuccess` (a protective refusal is not shown as a failure).
