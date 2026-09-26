# rigfile/base-secure v1

The always-on safety layer (RIGFILE_PLAN.md §8). It is embedded in the rigfile binary (`base-secure/`), extracted to a private temporary directory for each run and applied under every rig. Its items are **locked**: a rig may add to them, never replace, remove or disable them, and no directory layer or rig named `rigfile/*` can stand in for it. The only bypass is `--i-understand-unsafe-base` on `plan`/`apply`: local only, loudly announced, recorded in `state.json`, shown red by `rigfile doctor` until a normal apply (with `--update-lock`) restores it, and never accepted by a future `publish`.

## What it puts on your machine

| Layer | Where | What it does |
|---|---|---|
| Deny rules | `~/.claude/settings.json` `permissions.deny` | `Read` of `.env*`, keys (`*.pem *.key *.p12 …`, `id_*`), `service-account*.json`, `*.tfstate`, `~/.ssh ~/.aws ~/.config/gcloud ~/.azure ~/.kube ~/.gnupg`, registry/token files, Claude Code's own login files, Rigfile's state and hooks; `Edit` of Rigfile's own directories; `git … --no-verify`, `git … core.hooksPath`, force-push forms; `env`/`printenv`/`export -p`. Claude Code applies Read denies to Edit/Write on the same path and to recognised Bash readers (`cat head tail sed tee`, redirects). |
| Ask rules | same, `permissions.ask` | `git push` (all forms), `rm -rf`, `sudo`, recursive `chmod 777`, package publish, privileged containers, `curl … \| sh`, edits to the agent's own `settings.json`. |
| Guard hook | PreToolUse on Bash | Unwraps `sh -c`, `eval`, `env`, `sudo`, `timeout`, `nice`, `xargs`, path-qualified programs, `git -C/-c`, then applies the rules above with the full command text (deny/ask with a reason the agent can act on). |
| Write guard | PreToolUse on Write / Edit / MultiEdit / NotebookEdit | Full secret scanner over the content being written. Deny (ask when the target is git-ignored); the message names the rule, never the value. |
| Redact | PostToolUse, all tools | Secret-looking text in tool output is replaced by `[REDACTED:<rule>]` before the model reads it (`updatedToolOutput`). |
| Setting | `permissions.disableBypassPermissionsMode` | Turns off `bypassPermissions` mode, which skips the protected-path prompts. Never overwrites a value you set yourself. |
| Instructions | `~/.claude/CLAUDE.md` marked section | The short "Security baseline" snippet. |
| Git | `~/.config/rigfile/git-hooks`, global gitconfig block, global excludes block | Pre-commit and pre-push secret scanning, the `--no-verify`-proof reference-transaction backstop, credential-file patterns in the global gitignore; your existing hooks are chained, not replaced (see `docs/targets/git.md`). |

## Opt-in: the Claude Code sandbox profile (`--sandbox`)

Off by default and remembered in `state.json` once chosen (`--no-sandbox` stops managing it). On macOS, Linux and WSL2 it adds `sandbox.enabled: true`, `sandbox.allowUnsandboxedCommands: false`, `sandbox.credentials.files` deny entries for the credential directories above and `sandbox.credentials.envVars` deny entries for common token variables (`AWS_SECRET_ACCESS_KEY`, `GITHUB_TOKEN`, `NPM_TOKEN`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, ...). It is the only layer that stops a *script* from opening `~/.ssh/id_rsa` itself. It only adds: your own `sandbox` values are never overwritten (the plan says so and exits 3), and on Linux the plan warns, with the exact install command it will not run itself, when `bubblewrap`/`socat` are missing (Claude Code would otherwise silently run commands unsandboxed). Not available on native Windows. Turning it off removes the entry lists; the scalar settings stay until you delete the `sandbox` block (the plan says where). Real enforcement is UNVERIFIED until the live procedure runs (`docs/targets/claude-code.md` §12.7).

## What it cannot do (be honest about the gaps)

- **Deny rules match command text.** Claude Code's docs list forms a rule misses (`/bin/rm`, `sh -c '…'`, `git -C . push`); the guard hook covers those, but a tokenizer is not a shell. **Known evasions** (asserted by `TestKnownEvasionsStayDocumented`, so this list cannot drift): scripts in another language that run the command (`python -c "subprocess.run(['git','commit','--no-verify'])"`), variable indirection (`X=--no-verify; git commit $X`), encoded payloads (`echo … | base64 -d | sh`), aliases, `find … -exec cat {} +`, and a Python/Node script that opens a credential file itself. The OS-level answer is the Claude Code sandbox (S2-M5b, opt-in).
- **Hooks can be turned off** by the user (`disableAllHooks`, `--settings`), and user-level settings can be overridden by managed settings. base-secure is a guardrail against agent mistakes and agent misbehaviour, not a control against a determined local user.
- **`git push --no-verify` skips pre-push.** The backstop stops a leaking *commit* from being created with `--no-verify`, but a commit made before the hooks were installed can still be pushed with `--no-verify`.
- **Redaction shape is UNVERIFIED**: the docs say `updatedToolOutput` (inside `hookSpecificOutput`) replaces a tool's text output for every tool, but not its exact value type or the `tool_response` shapes; the hook reads several shapes and always adds `additionalContext`. Confirmed only by the live red-team procedure (S2-M7(b)). Whether the on-disk transcript keeps the original text is also unverified.
- **Native Windows** file protection is weaker (no sandbox there) and Windows equivalents of the deny list arrive in Stage 3.
- `.env.example` cannot be excepted from a `Read` deny (allow cannot carve out of deny), so the deny list names the common `.env.*` files instead of `.env.*`; the hooks use the exact name rules, which do exempt `.env.example`.
