# Owner checklist: what's actually left

Rewritten 2026-09-28 to replace the old stage-by-stage version (17 files removed: 9 per-stage plans, 7 per-stage
owner-check documents, and the Stage 1 spike report; their still-open content is folded in below, their historical
content is in `docs/STATUS.md`, the ADRs, and git history). Everything in this file needs you specifically — an
account, money, a real machine, a person, or a decision. What I can do myself, I do without asking; this file is
only what's left after that.

Current shape of the project, in one paragraph: all 8 build stages (plus 3b, local models) are merged to `main`.
Sharing through GitHub (`publish`'s own default destination, `pull github.com/owner/repo`) is the primary, working way to share a rig,
decided 2026-09-28. The registry is a real, tested, optional feature, currently **not deployed** (the Fly/Neon/R2
setup from 2026-09-27 was torn down 2026-09-28 — see `docs/registry.md` §8 for what deploying it again would take).
The GitHub repository is **private by owner choice**, not yet pushed past `stage-0-spec`-era history.

## 1. Before making the repository public

This is the actual remaining blocker to "publish." Everything else below is real work, but none of it blocks
open-sourcing the repo as it stands.

| # | Step | Status |
|---|---|---|
| 1 | Git history audit (secrets, personal data) | **done**: gitleaks clean across all 158 commits on every branch; test data and the one fixture built from a real capture were replaced with made-up equivalents (`docs/STATUS.md` "Pre-publish history audit") |
| 2 | A real `README.md`, working docs | **done**: `README.md`, `docs/guide/` |
| 3 | Push `main` to `origin`, make it the default branch (currently `stage-0-spec`) | **open — needs you**: my pushes are denied by design |
| 4 | Make the repository public | **open — needs you**, a deliberate decision, not yet made |
| 5 | Delete (or tag, then delete) the merged `stage-*`/`integration`/`fix-ci-integration` branches — all fully merged into `main` except `stage-2`, which has one commit `main` doesn't (a superseded doc/test tweak, safe to drop) | **open — needs you** |
| 6 | Enable private vulnerability reporting on the repo (GitHub Settings → Security) once public | **open — needs you** |
| 7 | First real CI run since the repo went private | **done** (2026-09-28): first pushes surfaced a gitleaks-license issue (org repos need a paid license for the Action wrapper — switched to running the real binary directly), a git-identity gap in the new publish-to-GitHub tests, a Windows path-separator mismatch in one test assertion, and what looked like a Linux Landlock confinement bug but turned out to be a test-harness gap (below) — all fixed, and Linux confinement is enabled and verified, not disabled. CI is green. |

## 2. For me — I can keep going on these without you

- Write a step-by-step restore runbook for the registry's database (which Neon branch/timestamp to pick, how to cut `RIGFILE_REGISTRY_DATABASE_URL` over on Fly, who decides) — useful whenever it's redeployed, not urgent while it's down. Say the word.
- Anything else you want built, written or reviewed — ask.

## 3. For you — accounts, money, other people, decisions

### Release and packaging
- **Release signing:** generate a minisign key, set the GitHub secrets/variable (`MINISIGN_SECRET_KEY`, `MINISIGN_PASSWORD`, `MINISIGN_PUBLIC_KEY`), create a `release` environment with yourself as required reviewer, keep an offline backup of the key (losing it means every installed binary's trust anchor needs replacing — write that incident plan before the first public release). Dry-run the `release` workflow on a scratch repo first.
- **Code signing:** Apple Developer ID ($99/yr) for macOS notarization; a Windows Authenticode cert or Azure Trusted Signing. Until then, installers rely on minisign + SHA-256 only (Gatekeeper/SmartScreen will warn).
- **Package channels**, once signing exists: Homebrew tap (`rigfile/homebrew-tap`), Scoop bucket, a winget PR, `npm publish`, `twine upload` to PyPI, hosting a signed apt repo. Reserve the `rigfile` name on npm and PyPI (plus `rigfile-cli`) now, before anyone else does — free, no signing needed. dnf/rpm packaging isn't built.
- **The 5-external-users exit bar** (original plan, still a reasonable bar for "ready"): 5-10 people, at least one per OS, each given the install steps and a rig to pull; record OS, commands run, what surprised them, what failed, in `docs/STATUS.md`. Run `rigfile doctor --git <repo>` on anything you publish while testing.

### Legal
- Lawyer review of the four draft pages (`internal/registry/web/legal/{terms,acceptable-use,takedown,privacy}.md`) before any public launch of a hosted registry — they're honest drafts with a visible "not reviewed" banner, fine for the current beta framing, not for a real launch.
- Decide who reads abuse reports and how fast, if the registry comes back (`rigfile-registry admin reports|resolve-report|takedown|disable-user|audit`).
- OAuth apps for vendor login flows (GitHub, GitLab, etc. — `internal/login`) aren't registered anywhere; each needs a public client id from that vendor.

### Real-machine verification (turns an UNVERIFIED into a fact)
Confirmed live already: macOS/Apple Silicon (full core loop, real Keychain, real `claude` CLI — `docs/STATUS.md` "Real-machine verification"), Codex CLI and Gemini CLI (real binaries via npx), local-models hardware detection, ARM Linux distros (Ubuntu/Fedora/Alpine/Debian natively), and **Windows 11 24H2 on ARM64** (2026-09-29, UTM/QEMU on Apple Silicon — this also covers most of the separate "Windows on ARM" gap below, not just the general Windows one): install from source, `validate`/`plan`, `secrets set` → confirmed in Windows Credential Manager (via `rigfile doctor`; the Credential Manager GUI itself wasn't separately cross-checked), `icacls` permissions correctly restricted to the user + SYSTEM/Administrators, `apply`, `doctor`, `diff` (no drift), `rollback --force`, and the full git-hook secret-blocking path (a real commit blocked, `--no-verify` separately blocked by the reference-transaction backstop) — all through Git for Windows. Full detail and two real bugs found (one fixed, one open) in `docs/platforms.md` §8.

Still open:
- **Windows 11**, same or another clean VM, standard (non-admin) user — what the 2026-09-29 run above did NOT cover:
  - Two concurrent `rigfile secrets set` (PowerShell `Start-Job`) losing no update.
  - `rigfile exec -- npx --version` running the `.cmd` shim, and an argument like `"a & calc"` never starting a second command.
  - A hook that fails blocking the commit from every client, not just PowerShell → git (Git Bash, a GUI git client).
  - The actual Claude Code app's own PowerShell tool deny/ask rules firing on `Get-ChildItem env:` (not attempted in the 2026-09-29 run — only rigfile itself was installed, not Claude Code).
  - `claude.exe`/`codex`/`cursor` present → `rigfile plan` says "configured" and applies to `%USERPROFILE%\.claude`, `.codex`, `.cursor`, and Claude Desktop's `%APPDATA%\Claude\claude_desktop_config.json` (or its Microsoft Store path).
  - `install.ps1` (never run by anyone), the `rigd`/model-server scheduled-task XML (`schtasks /Create /XML` with the UTF-16 file as generated — unverified it's accepted), `rigfile ui`, `rigfile sync` with real paths, `rigfile broker install` (Task Scheduler: `schtasks /Query /TN rigfile-rigd /XML`, log off and on).
  - **The interactive-TUI-prompt bug found 2026-09-29** (`docs/platforms.md` §8: `[a]pply` doesn't register keypresses under the legacy Console Host, only Windows Terminal) needs an actual root-cause and fix, not just the workaround of telling people to use Windows Terminal.
- **Linux**: `systemd --user` broker/model-server install; a machine without user systemd (`rigfile broker start` background mode); WSL2 path rules and the cross-boundary deny globs; Ollama's real pull id for `qwen3:8b` (expected `500a1f067a9f`, unverified against the library page).
- **Vendors, live**: Cursor and Claude Desktop (GUI apps, need more than a CLI check); `codex --oss --local-provider ollama -m ...`; Claude Code against Ollama (experimental); VS Code loading the generated `.vscode/mcp.json` and Copilot reading `copilot-instructions.md`; a GitHub source, a GitLab source, and a plain `ssh://` git URL end to end; any vendor login flow (`rigfile logins`) against a real account — none of this can be fabricated under this project's own rules.
- **`rigfile broker install` actually working per OS**: macOS — `rigfile broker install`, then `launchctl print gui/$(id -u)/com.rigfile.rigd`, log out and in, `rigfile broker status`. Linux — same with `systemctl --user status rigfile-rigd`; also try a machine with no user systemd (WSL1, some containers) and `rigfile broker start` instead. Either way, the service must be able to read the secret store without a prompt (an encrypted-file backend can't prompt headlessly, so it has to be run by hand there).
- **The broker, with a real MCP server** that uses a key (Alpaca, GitHub, Brave Search-style): Node's `fetch` honouring `NODE_USE_ENV_PROXY`, Python/curl/Go trusting the session CA, a streaming (SSE) response surviving interception, a real vendor API accepting the swapped key.
- **`rigfile ui`** in a real browser on all three OSes: the macOS Keychain *prompt* specifically (a UI interaction, not exercised headlessly) — does it name "rigfile"?
- **Private sync**, two real machines, a real private git repo: `init`, `join`, `approve`/`finish` (compare fingerprints), `track`, `push`, `pull`, `revoke`.
- **Real hardware this project doesn't have**: actual Raspberry Pi OS (arm64 Debian is the closest proxy tested so far, and passed), and the mlx-lm reference setup on real Apple Silicon with 16 GB+ (needs `uv`, a ~4.6 GB download — the dev host here only has 16 GB total and can't safely spare it).
- **Live third-party services**: the Sigstore trusted-root fetch over TUF from a real network; a real keyless signature made in GitHub Actions verifying as the publisher's; OSV lookups against real package names (including a known-malicious one); the static-analysis rules run against real-world rigs and scripts, not just the self-written fixture corpus.

### If the registry is redeployed
Everything above that needs a *live* registry specifically (as opposed to just the CLI): `/admin` and the diff/collection/organisation pages in a real browser; a hostile file (e.g. `<script>` in an instruction) rendering as inert text; a migration applied to a real, already-populated database (back it up first — migrations `0003`/`0004` changed both visibility SQL fragments every read goes through); watching `admin reports`/`admin audit` for real; the external security review below; the incident-runbook table-top rehearsal (`docs/incident-response.md` §6) on a staging instance.

- **External security review**, Stages 5-8 (the registry): `docs/registry-security.md` §5-7 list what to attack first. The riskiest code is the visibility-fragment pair in `internal/registry/store_rigs.go` (organisations), the broker's proxy/policy code, and the vault's signature chain. Self-review (mine) doesn't satisfy this.
- **Live red-team run** of `base-secure` against a real, logged-in Claude Code in a disposable VM (`docs/red-team.md` Part 2) — this one doesn't need the registry at all, just a real Claude Code; still open regardless of registry status.

## 4. Known gaps — told to you, not tasks

- **Broker session-borrowing**: `rigfile exec --confine` closes the case where the borrowing process is a server Rigfile itself launched — the project's actual threat model. Verified live on macOS (`sandbox-exec`) and, as of 2026-09-28, on real Linux Landlock ABI 4+ hardware too (`docs/rigd.md` §8) — an earlier CI failure that looked like broken Linux enforcement turned out to be a `go test` harness gap, not the real confinement code; fixed, and Linux confinement is enabled normally. A wholly separate process on your account that was **never** launched through `rigfile exec` can still read the broker token directly — not covered; would need OS-level separation (a second account, or a keychain ACL scoped to the token).
- Level 2 (the broker) doesn't cover Go programs on macOS/Windows (OS trust store, not `SSL_CERT_FILE`), Java, TLS-pinned clients, HTTP/2 to the child, or WebSockets — those need `broker exclude`.
- Private sync doesn't stop a storage provider withholding all new writes (looks like "nothing changed"), or traffic analysis (file counts/sizes/timing are visible to it).
- Local-model catalog stubs (32 GB Apple, NVIDIA llama.cpp) are unapplied; no model gateway; `routing:` is parsed but not applied.
- Star counts can be bought; the static-analysis and similar-name rules are heuristics measured on a self-written corpus (see `docs/analysis-metrics.md`), not on real attackers.
- Organisation members are fully trusted to publish under the org's name — no approval step, no invitations, no transfer of a personal rig into one.
- Windows support throughout is compiled, vetted and unit-tested through injected environments, but **no line of it has run on a real Windows machine yet** (top of the list in §3).
