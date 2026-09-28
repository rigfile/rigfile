# Owner checklist: everything left that needs you

One list, drawn from the per-stage owner-check documents (linked in each row). Nothing here can be done by me: it needs an account, money, a real machine, a person, or a decision. Written 2026-09-26 on branch `integration`.

## Go-live checklist (2026-09-27)

Decided 2026-09-27: **the GitHub repository stays private for now**, and the registry site stays up as it is (its GitHub links and `git clone` step only work once the repo is public).

| # | Item | Status |
|---|---|---|
| 1 | Git history audit | **done**: gitleaks over all 158 commits on every branch, no secrets; all commit authors use the GitHub no-reply address; the owner's name in test data replaced by a made-up user (`ada`), and the fixture built from the owner's real setup replaced by the synthetic `testdata/fixtures/sample-rig` (old commits keep the old values; history not rewritten) |
| 2 | Real `README.md` | **done**: new README; the old Python scaffold and a stray empty `main` file deleted |
| 3 | Push `main`; make `main` the default branch (it is `stage-0-spec`, which has no Go code) | waits for release |
| 4 | Make the repo public; enable private vulnerability reporting; delete or tag the merged `stage-*` branches | waits for release |
| 5 | Fill the contact placeholders in the legal pages and `SECURITY.md` | open |
| 6 | Legal stance (beta with draft banner, or lawyer first); a privacy policy page | open |
| 7 | Registry: scale to one machine (the rate limiter is per process); auto-stop or always on | open |
| 8 | Monitoring and who reads reports | open |
| 9 | Written restore procedure | open |
| 10 | Deploy and live smoke test | open |
| 11 | First signed release; reserve npm/PyPI names; external security review; Windows check | after launch |

## 0. Unblock and merge (do first)

| # | Step | Notes |
|---|---|---|
| 0.1 | Fix GitHub billing (Settings → Billing & plans) | CI has not run since Stage 7 |
| 0.2 | Merge order: `stage-6` (if not yet) → `stage-8` → `stage-8b` → `stage-8c` → `stage-8d` → `stage-3b` **or** merge `integration`, which already has all of them with the conflicts resolved | `git push -u origin <branch>` for each; I read the CI runs |
| 0.3 | Read the first Windows CI run of each: the broker, `internal/svc`, hardware detection (`GetDiskFreeSpaceEx`), `internal/vault`, `rigfile ui`, the scheduled-task file | expect a round or two of path-separator style fixes |
| 0.4 | Database backup before deploying registry migrations 0003 and 0004 (they change the two visibility SQL fragments every read uses) | `docs/stage-8-owner-checks.md` §2 |

## A. Decisions only you can make

| Decision | Recommendation | Where |
|---|---|---|
| ~~LICENSE~~ | **Decided 2026-09-26: Apache-2.0** (`LICENSE`; package manifests carry it). Still open: the copyright holder line for a `NOTICE`, if you want one | `docs/stage-4-owner-checks.md` §2 |
| ~~Package names, `.deb` maintainer~~ | **Decided 2026-09-26:** org `rigfile` (created, repo transferred to `rigfile/rigfile`); winget `Rigfile.Rigfile`; tap `rigfile/homebrew-tap`; `.deb` maintainer `Rigfile maintainers <bytebuilderslab@gmail.com>`. Yours to do: reserve npm `rigfile` and PyPI `rigfile` (plus `rigfile-cli`), create the tap repo | same |
| Registry: ~~hosting~~ (decided 2026-09-26: managed platform, one machine, managed Postgres with point-in-time restore, Cloudflare R2; staging first), ~~domain~~ (decided: `rigfile.bytebuilderslab.app`, DNS at the owner's registrar; sessions use `__Host-` cookies so sibling subdomains cannot interfere). ~~GitHub-rename hijack~~ (decided: reserve vacated logins that own rigs; built, migration 0005, `admin release-login`). ~~retention of rejected uploads~~ (decided: delete at once; reason kept). ~~GitHub OAuth app~~ (created 2026-09-26, client id `Ov23licYmVqaKBqv1rNb`). ~~admins~~ (decided: sole admin `digitaldreamer3462`, checked on-demand — no alerting exists); one instance only until a shared rate limiter exists | | `docs/stage-5-owner-checks.md` §2 |
| ~~Popular-rig threshold, verified publisher, `/admin` reader~~ | **Decided 2026-09-26:** threshold stays 0; verified-publisher criteria deferred to the first real request; `/admin` is the owner, on-demand (same as decision 3) | `docs/stage-6-owner-checks.md` §2 |
| ~~Organisations~~: who may create one; are members fully trusted to publish | **Confirmed 2026-09-26: keep as built** — open creation (capped 10/user), members fully trusted, `disable-org` as the reactive brake | `docs/stage-8-owner-checks.md` §3 |
| ~~Broker~~: opt-in Level 2; fail closed when the broker is down | **Confirmed 2026-09-26: keep as built.** Session-borrowing: **decided 2026-09-27** — built `rigfile exec --confine` (sandbox-exec on macOS, Landlock on Linux), closing the case where the borrowing process is a server Rigfile itself launched (the project's actual threat model). Verified live on macOS; **Linux confinement is UNVERIFIED** (needs Landlock ABI 4 / kernel 6.7+, unavailable on this project's own dev/CI machines) — fails closed there instead. A wholly separate, non-launched process reading the token file directly stays open; needs OS-level separation (account or keychain ACL), still not built | `docs/stage-7-owner-checks.md` §2, `docs/rigd.md` §8 |
| ~~Local models~~: re-scoped exit criterion (Ollama-only, no bridge); Codex only on Ollama's default port; Claude Code experimental; catalog stubs unapplied | **Confirmed 2026-09-27: accept as built.** The mlx-lm bridge was re-examined (re-checked against OpenAI's current docs): it needs a real Responses-API-speaking (Codex) and Anthropic-API-speaking (Claude Code) translation server in front of `mlx_lm.server`, not a config change — `--oss`'s local provider is a closed `ollama\|lmstudio` enum, mlx-lm isn't in it. Deferred as its own future milestone, not built now | `docs/stage-3b-owner-checks.md` §2, `docs/targets/codex.md` |
| ~~Private sync~~: per-project Claude Code memory, a recovery passphrase | **Decided 2026-09-27: skip both.** Owner does not use Claude Code project memory across machines today; revisit (verify the vendor's memory layout first) if that changes. Recovery passphrase stays off by default, as built | `docs/private-sync.md` §7 |
| ~~Zed~~: **built 2026-09-27** — `internal/adapters/zed`, stdio and remote MCP servers, JSONC-safe; Windows path still open. ~~Devin (formerly Windsurf)~~: **built 2026-09-27** — `internal/adapters/devin`, stdio MCP servers only, user-scope; remote servers, instructions/rules and the Windows path still open (`docs/targets/devin.md`) | | `docs/targets/devin.md`, `zed.md` |

## B. Accounts, money, other people

- **Release signing:** generate the minisign key, set the GitHub secrets and variable, create the `release` environment, keep an offline backup, write the key-loss incident plan (`docs/stage-4-owner-checks.md` §3). Dry-run the `release` workflow on a scratch repository first.
- **Code signing:** Apple Developer ID ($99/yr) and a Windows Authenticode/Azure Trusted Signing identity (§4 there). Until then installers rely on minisign and SHA-256.
- **Package channels:** Homebrew tap, Scoop bucket, winget PR, npm, PyPI, apt hosting; dnf/rpm is not built (§5 there).
- ~~**Deploy the registry**~~ **done 2026-09-27**: live at `https://rigfile.bytebuilderslab.app` (Fly.io + Neon + Cloudflare R2); real GitHub sign-in, a real `publish --to-registry`, a second real (non-admin) account publishing and a third anonymous account pulling (public succeeds, the owner's private rig is correctly refused) plus a genuine second GitHub account (`rigfile-bot`) confirming the same thing signed in for real, and a Neon point-in-time restore drill are all now confirmed live (`docs/stage-5-owner-checks.md` §3). Still open: a written incident procedure for an actual restore (which branch/timestamp, how to cut over the connection string, who decides).
- **Lawyer:** terms, acceptable use, takedown and a privacy policy (the legal pages are drafts; add the newer stored facts: verification notes, signer identities, first-seen dates, and the audit log of hosts the broker keeps only locally) (`docs/stage-5-owner-checks.md` §4, `docs/stage-6-owner-checks.md` §5).
- **OAuth apps** for the vendor login flows (no client ids are registered) (`docs/stage-4-owner-checks.md` §6.7).
- **Abuse handling:** who reads reports, how fast, what you escalate (`docs/stage-5-owner-checks.md` §5).

## C. External review and rehearsal

1. **External security review** of Stages 5 to 8: `docs/registry-security.md` §5 to §7 list what to attack first. The riskiest change is the pair of visibility fragments in `internal/registry/store_rigs.go` (organisations); also the broker's proxy and policy code, and the vault's signature chain.
2. **Incident-runbook rehearsal:** the three table-top exercises in `docs/incident-response.md` §6, on a staging registry. Fill the bracketed contacts first.
3. **Live red-team run** of `base-secure` against a real Claude Code in a disposable VM (`docs/red-team.md` Part 2).

## D. Real-machine checks (each turns an UNVERIFIED into a fact)

**Windows 11 clean VM** (`docs/stage-3-owner-checks.md` §2): Credential Manager, DACLs on state files, `.cmd` shim launching, hooks through Git for Windows, the PowerShell tool rules, `rigfile exec` with hostile arguments; plus `install.ps1` (never executed by anyone), the scheduled-task XML for `rigd` and model servers (does `schtasks /Create /XML` accept the UTF-16 file as generated?), `rigfile ui`, `rigfile sync` with real paths.
**macOS clean VM / Apple Silicon:** ~~the Tart procedure in `e2e/README.md`~~ **done 2026-09-27** (`e2e/run-macos.sh`, real Tart VM, real Apple Silicon): validate/plan/apply/idempotent/secrets/exec/doctor/diff/rollback all pass with the **real Keychain** (not the encrypted-file fallback), and the real `claude` CLI's `mcp add-json/get/remove --scope user` behaviour matches every assumption ADR 0002 made (now updated with the real output). Still open: `rigfile broker install` (launchd) and `rigfile ui` with the real keychain *prompt* (a UI interaction, not exercised headlessly); **local models:** the reference setup end to end (needs `uv`; ~4.6 GB download), the generated `mlx_lm.server` command, `doctor`'s tool-call test (`docs/stage-3b-owner-checks.md` §3).
**Linux and WSL2:** `systemd --user` units, a machine without user systemd (`rigfile broker start` background mode), WSL path rules (`docs/stage-3-owner-checks.md` §4), Ollama pulls and its id equalling `500a1f067a9f` for `qwen3:8b`.
**Vendors, live** (`docs/stage-3-owner-checks.md` §5, `docs/stage-4-owner-checks.md` §6): ~~Codex~~ and ~~Gemini CLI~~ **confirmed 2026-09-27** actually load what Rigfile writes (real CLIs via npx, no login needed for `mcp list`/`doctor`; `docs/targets/codex.md` §6, `gemini-cli.md`) — Codex's MCP server and base-secure sandbox/approval settings both read correctly, no gotchas; Gemini CLI's MCP shape is correct too, but it silently hides even user-scope servers in an untrusted folder (now a plan-screen note). Still open: Cursor, Claude Desktop (both GUI apps, need more than a CLI check); `codex --oss --local-provider ollama -m …` together; Claude Code against Ollama (experimental); VS Code loading the generated `.vscode/mcp.json` and Copilot reading `copilot-instructions.md`; GitHub and GitLab sources, ssh URLs; vendor logins (all of these need a real login/subscription this project's rules do not permit fabricating).
**Real MCP servers through the broker** (Alpaca, GitHub, Brave Search): Node's `fetch` following `NODE_USE_ENV_PROXY`, Python and curl trusting the session CA, streaming responses, and a vendor API accepting a swapped key (`docs/stage-7-owner-checks.md` §3).
**Live services:** the Sigstore trusted-root fetch (TUF), a real keyless signature from GitHub Actions, OSV lookups on real packages, the static-analysis rules against real-world rigs (`docs/stage-6-owner-checks.md` §3).
**Browser checks:** the registry pages (diffs, collections, organisations, a hostile file shown as text), `/admin` actions (`docs/stage-6-owner-checks.md` §3.5, `docs/stage-8-owner-checks.md` §4).
**Private sync:** two real machines and a private git repository: `init`, `join`, `approve` (compare the fingerprints), `finish`, `track`, `push`, `pull`, `revoke` (`docs/private-sync.md` §7).
**ARM and other distros** (Raspberry Pi OS arm64, Alpine, Windows on ARM): `self-update`, `apply`, `broker run`. Progress 2026-09-27: `e2e/run.sh` now runs Ubuntu, Fedora, Alpine and Debian natively on real arm64 hardware (this is an Apple Silicon dev machine); Alpine confirmed honest degradation on musl/busybox with no `apk` catalog entries; Debian is the closest safe proxy for Raspberry Pi OS available without real Pi hardware, and passed. Still open: real Raspberry Pi hardware itself, and Windows on ARM.

## E. Known gaps you should be told about, not tasks

- The broker cannot stop a process of yours from borrowing another approved server's session and spending that key at its own host (`docs/red-team-broker.md`).
- Withholding all new writes, and traffic analysis, are not stopped by private sync.
- Level 2 does not cover Go programs on macOS and Windows (OS trust store), Java, pinned clients, HTTP/2 to the child, WebSockets.
- Stubs never applied: the 32 GB Apple and NVIDIA llama.cpp catalog entries; no gateway, no `routing:` translation.
- Star counts can be bought; the analysis and similar-name rules are heuristics measured on a self-written corpus.
- Organisation members are fully trusted to publish under the organisation's name; there is no approval step, no invitations, no transfer of a personal rig.
