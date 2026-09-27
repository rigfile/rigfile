# Owner checklist: everything left that needs you

One list, drawn from the per-stage owner-check documents (linked in each row). Nothing here can be done by me: it needs an account, money, a real machine, a person, or a decision. Written 2026-09-26 on branch `integration`.

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
| Registry: domain, hosting, bucket (R2 or S3), GitHub OAuth app, admins, retention of rejected uploads, GitHub-rename hijack policy, one instance only until a shared rate limiter exists | | `docs/stage-5-owner-checks.md` §2 |
| Popular-rig threshold, who counts as a verified publisher, who reads `/admin` and how often | leave the threshold 0 until you have signing publishers | `docs/stage-6-owner-checks.md` §2 |
| Organisations: who may create one; are members fully trusted to publish (no approval step) | keep open with `disable-org` as the brake | `docs/stage-8-owner-checks.md` §3 |
| Broker: opt-in Level 2; fail closed when the broker is down; accept "borrow another approved server's session" as not stopped, or invest in OS-level separation (broker under another account, or a keychain ACL for the token) | accept for now | `docs/stage-7-owner-checks.md` §2, `docs/rigd.md` §8 |
| Local models: re-scoped exit criterion (Ollama-only agent wiring, no bridge); Codex only on Ollama's default port | accept | `docs/stage-3b-owner-checks.md` §2 |
| Private sync: per-project Claude Code memory (needs the vendor's memory layout verified first), a recovery passphrase | skip both until wanted | `docs/private-sync.md` §7 |
| Windsurf, Zed targets: paths conflict or are unverified | run the "what settles it" step in each file, then tell me the paths | `docs/targets/windsurf.md`, `zed.md` |

## B. Accounts, money, other people

- **Release signing:** generate the minisign key, set the GitHub secrets and variable, create the `release` environment, keep an offline backup, write the key-loss incident plan (`docs/stage-4-owner-checks.md` §3). Dry-run the `release` workflow on a scratch repository first.
- **Code signing:** Apple Developer ID ($99/yr) and a Windows Authenticode/Azure Trusted Signing identity (§4 there). Until then installers rely on minisign and SHA-256.
- **Package channels:** Homebrew tap, Scoop bucket, winget PR, npm, PyPI, apt hosting; dnf/rpm is not built (§5 there).
- **Deploy the registry** (staging first), real GitHub sign-in, backups **and a tested restore** (`docs/stage-5-owner-checks.md` §3).
- **Lawyer:** terms, acceptable use, takedown and a privacy policy (the legal pages are drafts; add the newer stored facts: verification notes, signer identities, first-seen dates, and the audit log of hosts the broker keeps only locally) (`docs/stage-5-owner-checks.md` §4, `docs/stage-6-owner-checks.md` §5).
- **OAuth apps** for the vendor login flows (no client ids are registered) (`docs/stage-4-owner-checks.md` §6.7).
- **Abuse handling:** who reads reports, how fast, what you escalate (`docs/stage-5-owner-checks.md` §5).

## C. External review and rehearsal

1. **External security review** of Stages 5 to 8: `docs/registry-security.md` §5 to §7 list what to attack first. The riskiest change is the pair of visibility fragments in `internal/registry/store_rigs.go` (organisations); also the broker's proxy and policy code, and the vault's signature chain.
2. **Incident-runbook rehearsal:** the three table-top exercises in `docs/incident-response.md` §6, on a staging registry. Fill the bracketed contacts first.
3. **Live red-team run** of `base-secure` against a real Claude Code in a disposable VM (`docs/red-team.md` Part 2).

## D. Real-machine checks (each turns an UNVERIFIED into a fact)

**Windows 11 clean VM** (`docs/stage-3-owner-checks.md` §2): Credential Manager, DACLs on state files, `.cmd` shim launching, hooks through Git for Windows, the PowerShell tool rules, `rigfile exec` with hostile arguments; plus `install.ps1` (never executed by anyone), the scheduled-task XML for `rigd` and model servers (does `schtasks /Create /XML` accept the UTF-16 file as generated?), `rigfile ui`, `rigfile sync` with real paths.
**macOS clean VM / Apple Silicon:** the Tart procedure in `e2e/README.md`; `rigfile broker install` (launchd) and `rigfile ui` with the real keychain prompt; **local models:** the reference setup end to end (needs `uv`; ~4.6 GB download), the generated `mlx_lm.server` command, `doctor`'s tool-call test (`docs/stage-3b-owner-checks.md` §3).
**Linux and WSL2:** `systemd --user` units, a machine without user systemd (`rigfile broker start` background mode), WSL path rules (`docs/stage-3-owner-checks.md` §4), Ollama pulls and its id equalling `500a1f067a9f` for `qwen3:8b`.
**Vendors, live** (`docs/stage-3-owner-checks.md` §5, `docs/stage-4-owner-checks.md` §6): Codex, Gemini CLI, Cursor, Claude Desktop actually load what Rigfile writes; `codex --oss --local-provider ollama -m …` together; Claude Code against Ollama (experimental); VS Code loading the generated `.vscode/mcp.json` and Copilot reading `copilot-instructions.md`; GitHub and GitLab sources, ssh URLs; vendor logins.
**Real MCP servers through the broker** (Alpaca, GitHub, Brave Search): Node's `fetch` following `NODE_USE_ENV_PROXY`, Python and curl trusting the session CA, streaming responses, and a vendor API accepting a swapped key (`docs/stage-7-owner-checks.md` §3).
**Live services:** the Sigstore trusted-root fetch (TUF), a real keyless signature from GitHub Actions, OSV lookups on real packages, the static-analysis rules against real-world rigs (`docs/stage-6-owner-checks.md` §3).
**Browser checks:** the registry pages (diffs, collections, organisations, a hostile file shown as text), `/admin` actions (`docs/stage-6-owner-checks.md` §3.5, `docs/stage-8-owner-checks.md` §4).
**Private sync:** two real machines and a private git repository: `init`, `join`, `approve` (compare the fingerprints), `finish`, `track`, `push`, `pull`, `revoke` (`docs/private-sync.md` §7).
**ARM and other distros** (Raspberry Pi OS arm64, Alpine, Windows on ARM): `self-update`, `apply`, `broker run`.

## E. Known gaps you should be told about, not tasks

- The broker cannot stop a process of yours from borrowing another approved server's session and spending that key at its own host (`docs/red-team-broker.md`).
- Withholding all new writes, and traffic analysis, are not stopped by private sync.
- Level 2 does not cover Go programs on macOS and Windows (OS trust store), Java, pinned clients, HTTP/2 to the child, WebSockets.
- Stubs never applied: the 32 GB Apple and NVIDIA llama.cpp catalog entries; no gateway, no `routing:` translation.
- Star counts can be bought; the analysis and similar-name rules are heuristics measured on a self-written corpus; Windsurf and Zed are not supported.
- Organisation members are fully trusted to publish under the organisation's name; there is no approval step, no invitations, no transfer of a personal rig.
