# Rigfile — Claude Code project instructions

Master plan: `RIGFILE_PLAN.md`. Read it before starting work. Current stage and progress: `docs/STATUS.md`.

**Current stage: Stage 0 (research & spec freeze). No CLI code. Use public documentation only — never read the developer's real `~/.claude`, `~/.codex`, or other config folders.**

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

## Stage 0 additions

- Research output must cite an official source link and the date checked. Where docs are unclear or disagree with the plan, write **UNVERIFIED** or **CONFLICT** — do not guess.
- No git commits unless the owner asks.
