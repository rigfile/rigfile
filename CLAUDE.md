# Rigfile — Claude Code project instructions

Master plan: `RIGFILE_PLAN.md`. Read it before starting work. Current stage and progress: `docs/STATUS.md`.

**Current stage: Stage 1 (local CLI, Claude Code only, macOS + Linux; Windows is Stage 3). Language: Go (ADR 0001). The build is complete on branch `stage-1`; see `docs/STATUS.md` for what awaits owner sign-off.**

Never read or copy from the developer's real `~/.claude`, `~/.codex` or other config folders yourself. The only exception is a capture the owner runs (`rigfile init`, read-only) into a scratch dir, reviewed by the owner before anything enters the repo. Vendor formats still come from official docs.

## Working in this repo

- Go toolchain: `export PATH=/opt/homebrew/bin:$PATH` if `go` is not found. Before committing: `gofmt -l .` (must print nothing), `go vet ./...`, `go test -race ./...`. Also `GOOS=windows go vet ./...` (Windows must keep compiling).
- Layout: `cmd/rigfile` (CLI), `internal/{manifest,merge,layers,lock,state,apply,engine,session,adapters/claudecode,tools,secrets,execshim,hook,scan,platform,splice,jsonedit,hashing}`, `catalog/`, `schema/`, `e2e/` (container tests: `e2e/run.sh`), `docs/`.
- Tests use a temp `$HOME` and injected fakes (`env` in `cmd/rigfile`, `MCPClient`, `tools.Host`); never touch the real machine, keychain or package managers.
- The `claude` CLI's output formats are undocumented (ADR 0002): keep that logic behind `MCPClient`.
- Commits: conventional (`feat(mN):`, `fix(...)`), end with the Co-Authored-By line the harness gives. Commit at the end of each coherent change or milestone without asking (owner, 2026-09-25); pushing stays with the owner (my pushes are denied; hand over the command). The global gitleaks hook runs on commit; fix what it flags (build fake secrets at run time), never bypass it.
- Do not work around permission or classifier denials; report them and let the owner run the command.

## Working agreements (RIGFILE_PLAN.md §16)

1. **One stage at a time.** Work only on the current stage in `docs/STATUS.md`. Propose, don't start, next-stage work.
2. **Verify vendor formats** from official docs before writing an adapter; record source + date in `docs/targets/`.
3. **Never use real secrets** in tests, fixtures, or examples. Use obviously fake values from `testdata/secrets-corpus/` (e.g. `sk-ant-TESTTESTTEST…`).
4. **Never write to the developer's real `~/.claude`, `~/.codex`, etc. in tests.** All tests use a temp `$HOME` (and temp `%USERPROFILE%`/`%APPDATA%` on Windows). E2E runs in VMs.
5. **No OS assumptions outside `internal/platform`.** No hard-coded `/`, `~`, `/Users/`, `C:\`, `brew`, or `sh` anywhere else; use `filepath` and platform APIs. CI must pass on macOS, Linux, and Windows before merging. (The package name and language are subject to ADR 0001; the rule itself is language-independent: one platform module owns all OS differences.)
6. **Every file write goes through the backup + structured-merge path.** No direct overwrites.
7. **Security-sensitive code** (secrets, scan, login, apply, hooks) requires tests and a short threat note in the PR description.
8. **Small PRs**, conventional commits, update `docs/STATUS.md` at the end of each session.
9. **Dogfood:** Rigfile's own repo uses `rigfile/base-secure` from Stage 2 onward.

## Stage 0 rules (still apply to research and docs)

- Research output must cite an official source link and the date checked. Where docs are unclear or disagree with the plan, write **UNVERIFIED** or **CONFLICT** — do not guess.
- Commits: see "Working in this repo" (the owner delegated committing on 2026-09-25).
