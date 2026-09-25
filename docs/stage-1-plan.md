# Stage 1 plan of record

**Goal (RIGFILE_PLAN.md §12):** recreate the owner's Claude Code setup on a fresh machine from a local folder, on macOS and Linux, with no network service. Windows compiles and returns "not yet supported".
**Exit:** on a clean macOS VM and a clean Ubuntu VM, `rigfile apply ./jia-rig` + entering 2 API keys + 1 Claude login = a working setup identical to the owner's (verified by `doctor` and a manual smoke test). Zero plaintext secrets found by a scan of the home directory. Headless Linux works via the encrypted-file fallback.

Starting point: the spike (`docs/spike-report.md`): platform, manifest validation, splice, JSON edit, permissions adapter, backup-first writes, secrets, exec shim, PreToolUse guard, minimal CLI. Decisions already made: Go (ADR 0001), user-scope MCP via `claude mcp add-json` (ADR 0002), keychain-only backend with encrypted-file fallback, merge semantics v1.

## Milestones

Each milestone is a set of small commits, ends with green tests on the host, and updates this file's status column. "Owner gate" = something only the owner can do or decide.

| # | Milestone | Delivers | Acceptance | Status |
|---|---|---|---|---|
| M1 | **Model and merge engine** | Full manifest types; load from a rig directory; layer merge per `docs/merge-semantics.md` (ids, replace, locked items, permissions union/no-removal, secrets `hosts` narrowing, tools pins, overrides); `os:`/`targets:` projection with a "not applicable here" list | Every worked example (§7 A–E) is a test; merge is a pure function; same lockfile hash on any OS | **done** (`internal/manifest` model+Check, `internal/merge`, `internal/layers`) |
| M2 | **State, lockfile, rollback, diff** | `state.json` (what Rigfile owns, per target/category/item, with content hashes), `rigfile.lock` (layer + file hashes), run manifests for backups, `rollback [id]`, `diff` (drift) | apply→diff clean; hand-edit → diff shows it; rollback restores exact bytes; lockfile mismatch refuses to apply | **infrastructure done** (`internal/hashing`, `lock`, `state`, apply journal + `Rollback`); the `diff`/`rollback`/`lock` commands are wired in M4 |
| M3 | **Claude Code adapter, all categories** | instructions (marked sections in `~/.claude/CLAUDE.md`), skills, agents, commands (directory/file writes with hashes), hooks (settings.json), MCP servers (ADR 0002 + exec-shim rewrite), permissions (done); each with `plan`, `apply`, `verify`, `capture` | Golden files per category; round-trip `capture(apply(x)) == x` for supported fields; user content never modified | **plan/apply/verify done** for all seven categories (`internal/engine`, `internal/adapters/claudecode`); `capture` is `claudecode.Capture` (used by `init`) |
| M4 | **CLI commands** | `init` (capture), `plan <dir>`, `apply <dir>`, `diff`, `rollback`, `doctor`, `secrets list/set/rm`, `exec` (done). One review screen (plan §5.1), `[a]pply all / [q]uit` | Plan output matches §5.1 for the fixture; `--yes`; exit codes documented | **done**: `init` (read-only capture; secrets never copied; skips what Rigfile already manages), `validate`, `plan`, `apply`, `diff`, `rollback`, `lock`, `doctor`, `secrets`, `exec`, `hook` with end-to-end tests (`cmd/rigfile/main_test.go`); exit codes 0/1/2/3 documented in `main.go` |
| M5 | **Tools installer** | Catalog loader (embed `catalog/tools.yaml`), package-manager detection, install plan, brew / npm / pipx / uv executed after approval; apt/dnf/pacman **printed as commands** (no implicit sudo); verify via `detect` | Fake package managers in tests; nothing runs before approval; unpinned entries flagged | **done** (`internal/tools`, `catalog/embed.go`; TOOLS section on the review screen, `--no-tools`; brew/cask/npm/pipx/uv/cargo/go run after approval; apt/dnf/pacman/zypper and Windows managers are printed only; repo-setup entries never auto-selected; failures reported, config still applied; rollback does not uninstall tools) |
| M6 | **Secrets completion and logins** | Cross-process file lock; `secrets list` index; batched "secrets needed" prompt at the end of `apply`; vendor-CLI login step (`claude` login) | Two concurrent writers cannot corrupt the store; missing secret → clear, batched prompt | **done**: advisory `flock` around the file store's read-modify-write (test: 12 concurrent writers, none lost; Windows lock in Stage 3); apply ends with one batched hidden-input prompt for missing secrets (interactive only, empty = skip); login step prints the vendor's own sign-in command (`claude` /login, `gh auth login`); login *status* is not probed (vendor status commands unverified) |
| M7 | **CI** | GitHub Actions: macOS + Ubuntu full test matrix, Windows build-only; `gofmt`/`vet`/`-race`; secret-scan step | Green on all three OSes | **written** (`.github/workflows/ci.yml`: ubuntu+macos test matrix, Windows cross-build/vet, gitleaks); Windows build/vet verified locally; first green run needs a push (owner) |
| M8 | **E2E harness** | Container-based Ubuntu and Fedora runs ("fresh machine → apply → doctor green"); documented Tart procedure for macOS (owner-run) | Ubuntu container green; Fedora container green; macOS steps documented | todo |
| M9 | **The owner's rig** | Read-only capture of the owner's real `~/.claude` into a scratch dir, sanitised, reviewed by the owner, then committed as `testdata/fixtures/jia-rig/` | Owner reviews the sanitised diff before anything enters the repo | todo (owner gate) |

Ordering: M1 → M2 → M3 → M4 in sequence (each depends on the previous); M5, M6, M7 are independent of M3/M4 and may interleave; M8 needs M4; M9 needs M4 (`init`) and the owner.

## Owner gates (things I cannot do or must not decide alone)

1. Review of the sanitised capture of the owner's setup (M9) before it is committed.
2. Manual checks on real systems: real macOS Keychain, real Linux Secret Service (desktop and headless), `claude mcp add-json` against the real CLI, a real Claude login.
3. A clean **macOS VM** run (Tart/UTM) and a clean **Ubuntu VM/desktop keyring** run (the container run in M8 covers headless only).
4. Licence choice (§17 Q1) before the repo goes public or CI publishes artefacts.

## Standing rules (CLAUDE.md, restated for this stage)

Tests use a temp `$HOME` only; no real secrets; every write is backup-then-atomic-replace; no OS branching outside `internal/platform`; small commits; security-sensitive code gets a threat note; `docs/STATUS.md` updated at the end of each session.

## Risks tracked

| Risk | Mitigation |
|---|---|
| `claude mcp get/remove` behaviour undocumented (ADR 0002) | one interface, fake `claude` in tests, real-CLI smoke test as an owner gate |
| Vendor formats drift (Claude Code changes weekly) | feature-detect `claude --version`; golden files pin what we write; `docs/targets/claude-code.md` is the source of truth with dates |
| Instructions/hook edits touching user content | marker-splice only; drift detection; refuse on malformed markers |
| Cross-platform surprises (Linux keyring, Windows) | Linux via containers early (M8); Windows stubs fail closed |
