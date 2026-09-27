# Rigfile status

**Current stage: Stage 8 (growth features, first slice): S8-M0 to S8-M7 BUILT on branch `stage-8` (not pushed); the owner gate is `docs/stage-8-owner-checks.md`. Stages 1-7 are merged to `main`. Open for the owner: push stage-8 and read CI, a database backup before the two new migrations, the Stage 5-8 external security review, the runbook rehearsal, the Stage 7 live checks, and the decisions in the Stage 8 owner checks. Windsurf, Zed and VS Code Copilot are researched but not built (conflicting or unverified vendor paths); private memory sync, a hosted rig and ARM/distro verification are deliberately not started.**
Last updated: 2026-09-26 (Stage 8 first slice built)

## Stage 0 result (signed off)

Deliverables (all in `main`): repo skeleton and working agreements (`CLAUDE.md`); target research for Claude Code and Codex (`docs/targets/`); `docs/platforms.md`; `schema/rigfile.v1.json`; `docs/merge-semantics.md`; `catalog/tools.yaml`, `catalog/models.yaml`; `docs/adr/0001-language.md`; fixture `testdata/fixtures/plan-example.rigfile.yaml`.

Sign-off terms: the owner accepted the schema and the seven merge-semantics decisions (Appendix C of `docs/merge-semantics.md`) as recommended. The schema was validated mechanically (0 errors on the fixture; 29 accept/reject probes), not line-by-line reviewed by the owner; expect schema changes during Stage 1 (additive changes are cheap, breaking ones need a note here).

## Decisions log

| Date | Decision | Source |
|---|---|---|
| 2026-09-25 | **Language: Go**, conditional on the Stage 1 spike (fallback: Python `--onedir`) | ADR 0001 (Accepted) |
| 2026-09-25 | **Secret backend for Stage 1: OS keychain only** (macOS Keychain, Linux Secret Service, encrypted-file fallback for headless Linux). 1Password/Bitwarden later | plan §17 Q4 |
| 2026-09-25 | **OS order: macOS + Linux in Stage 1, Windows in Stage 3** (platform layer built for all three from day one; Windows stubbed) | plan §17 Q8 |
| 2026-09-25 | Merge-semantics decisions 1–7 accepted as written | `docs/merge-semantics.md` App. C |
| 2026-09-25 | **Go confirmed** after the spike (go/no-go: go) | `docs/spike-report.md`, ADR 0001 |
| 2026-09-25 | **User-scope MCP written via `claude mcp add-json --scope user`**, not by editing `~/.claude.json` | ADR 0002 |
| 2026-09-25 | Owner allows a **read-only `rigfile init` capture of their real `~/.claude`** into a scratch dir to build the first sanitized fixture; nothing enters the repo before the owner reviews it | owner, 2026-09-25 |

## Still open (not blocking the Stage 1 spike)

- §17 Q1 open source / licence: **no LICENSE file yet** (default = all rights reserved). Needed before the repo is public or CI is added. The repo already has a GitHub `origin` remote; nothing has been pushed from this environment.
- Q2 business model, Q5 private memory sync (default: defer to Stage 8), Q6 domains/names (schema `$id` is a placeholder), Q7 targets after Claude Code (Stage 3), Q9 WSL, Q10 code-signing, Q11 local-model scope.
- Plan corrections found in Stage 0 that the owner has not yet folded into `RIGFILE_PLAN.md` (see the "Summary of plan corrections" tables in `docs/targets/*.md` and `docs/platforms.md` §7): notably §9.5 local-model wiring, Codex hook trust, Codex/Claude Code permission syntax, `${CONFIG_DIR}`, credential files in the deny list.
- Not done in Stage 0: Cursor / Gemini CLI / Claude Desktop target docs (Stage 3); sanitized fixture of the owner's own setup (needs an export from the owner).

## Stage 1 spike — result (branch `stage-1-spike`, not merged)

Built and tested 2026-09-25: manifest validation, marker-splice editing, JSON layout-preserving edits, platform layer, Claude Code permissions adapter with backup-first writes, secrets (keychain + age fallback), exec shim, PreToolUse guard, CLI. 179 tests, race-clean, no skips on macOS. Hook ≈ 5 ms/call; release binaries 4.7–5.8 MB on darwin/arm64, linux/amd64, linux/arm64, windows/amd64. Details, findings and the not-verified list: `docs/spike-report.md`.
**Owner confirmed go.** Still open from the spike: real-Keychain and real-Linux checks, Linux/Windows latency, file-store lock.

## Stage 1 — build result (branch `stage-1`)

Milestones M1–M9 in `docs/stage-1-plan.md` are all done: manifest/merge/layers, lock/state/rollback, Claude Code adapter (all seven categories), CLI (`init validate plan apply diff rollback lock doctor secrets exec hook`), tools installer, secrets completion, CI workflow, container E2E (Ubuntu + Fedora pass), and the owner's rig fixture (`testdata/fixtures/jia-rig/`). `go test -race ./...` is green on macOS; Windows cross-builds and vets.

**Needs the owner before sign-off (I could not do these):**
1. ~~Push `stage-1` and confirm the first CI run is green~~ **Done 2026-09-25:** pushed; CI run 36199282279 on `d616a15` passed all five jobs (test ubuntu, test macos, windows cross-build, e2e ubuntu+fedora containers, gitleaks). The macOS runner uses the encrypted-file secret backend, not the real Keychain (item 2).
2. Real-Keychain and Linux Secret Service check (`rigfile secrets set` / `doctor` on a real desktop); macOS clean-VM run per `e2e/README.md`.
3. Smoke-test against the real `claude` CLI: `claude mcp add-json/get/remove --scope user` (ADR 0002 open items; tests use a fake).
4. LICENSE decision (§17 Q1); push `main` / set GitHub's default branch.
5. Optional: fold the docs correction about the marker format into `docs/merge-semantics.md` (region id is `<id>`, not `<layer>#<id>`).

Known limits: login status is not probed; tool installs are not undone by `rollback`; `init` skips nested command folders, symlinks and machine-specific MCP paths; base-secure layer arrives in Stage 2 (apply prints a note without it).

## Stage 1 — plan of record (original)

Goal (plan §12): recreate the owner's setup on a fresh machine from a local folder, on macOS and Linux.

1. **Spike (ADR 0001 §7), 2–3 days:** Go vertical slice the owner reviews, then go/no-go on Go.
   - `plan` for a Claude Code `settings.json` permissions merge (JSON key order preserved)
   - keychain set/get on macOS + Linux (desktop keyring and headless `age` fallback)
   - `rigfile hook pre-tool-use` shim, latency measured on macOS and Linux
   - marker-splice edit of a TOML file with comments
2. Then the Stage 1 build per plan §12 (platform layer, `init/plan/apply/diff/rollback/doctor/secrets/exec`, Claude Code adapter, backups, state, lockfile, tools installer, CI matrix, E2E in clean VMs).

Working rules for Stage 1 are in `CLAUDE.md` (small PRs, threat note for security-sensitive code, no real secrets, tests use a temp `$HOME`).

## Notes

- A Python scaffold (`pyproject.toml`, `src/rigfile`, `tests/`, `README.md`) existed before Stage 0 and was committed by the owner as `42bd6bc "stage 0"` (branch `stage-0-spec`, also pushed to `origin`). With Go chosen it is not the product; keep as dev tooling or delete: the owner's call. It is not part of the Stage 0 deliverables.
- `origin/HEAD` currently points at `stage-0-spec` (first branch pushed). Local `main` is created from it at sign-off; pushing `main` and making it GitHub's default branch is left to the owner.
- Global secret-scanning git hooks (gitleaks) are installed on the owner's machine outside this repo (`~/.config/git/hooks`); they run on commits and pushes here. This is a stopgap until base-secure (Stage 2).

## Known limits (Stage 1/2)

- `rigfile rollback` restores files but does not unregister MCP servers that `apply` added through the `claude` CLI; a re-apply after a rollback then reports "server exists and is not managed by Rigfile" (use `--overwrite`). Fix planned with the Stage 2 doctor/rollback work.
- Hook cost: on this macOS machine (each `git` call ~14 ms) pre-commit ~50 ms, commit-msg ~16 ms, agent hooks ~7 ms, the reference-transaction backstop ~0-8 ms per invocation (git calls it 5 times per commit, the `sh` shim filters most). A clean `git commit` with all hooks measured 60-150 ms in the Linux containers (noisy, includes `git add`). Go start-up (~6 ms) and git subprocesses dominate; rules already compile lazily.

## Stage 2: what the owner must do (S2-M9)

Exit criteria (plan §12) and the evidence:

| Criterion | Evidence |
|---|---|
| 100% of test-corpus secrets blocked at commit | Corpus: 223/223 core positives detected by the scanner (`docs/scanner-metrics.md`, gate in CI); the real pre-commit hook blocks a token, an encoded token, a credential file name and a secret in the commit message (`cmd/rigfile/githooks_e2e_test.go`, `docs/red-team.md` git layer) |
| All red-team prompts blocked or requiring approval | Deterministic suite: of 62 attack attempts, 39 are blocked and 15 ask first; the 8 evasions are documented (a further 8 rows are controls that must stay allowed) and each is covered by another layer or by the opt-in sandbox (`docs/red-team.md` Part 1). **Live run against a real Claude Code has not happened** (Part 2) |
| False positives documented and below the agreed threshold | 0 of 550 hard negatives (gate 1%, decision O2); the corpus grows whenever a real false positive turns up |

Your checklist, in order:

1. **Push and check CI:** `git push -u origin stage-2` (I cannot). The new `dogfood`, `e2e` (real git in Ubuntu/Fedora) and corpus/red-team gates run there.
2. **Read** `docs/base-secure.md` (what it enforces, what it cannot, threat note) and skim `docs/red-team.md`.
3. **Run the live red-team procedure** (`docs/red-team.md` Part 2) in a disposable VM/container with Claude Code logged in. It also settles the UNVERIFIED items: the PostToolUse `updatedToolOutput` shape and transcript contents, Write/Edit field names, whether `Read(~/.ssh/**)`/the sandbox stop a script, and `disableBypassPermissionsMode` in user settings.
4. **Apply base-secure to your own machine** (I never touch your real config): `rigfile apply <your rig>` (or `./cmd/rigfile`), read the GIT section of the plan (it appends a block to `~/.gitconfig`, adds hooks under `~/.config/rigfile/git-hooks` that chain to your existing `~/.config/git/hooks`, and adds a block to your global excludes), approve, then `rigfile doctor` and try `git commit` in a scratch repo. Optional: `--sandbox`.
5. **LICENSE** (§17 Q1) and merge `stage-2` into `main` when satisfied.

Then Stage 3 (Codex/Cursor/Gemini adapters + Windows) is next in the plan; it starts on your go.

Deviations from the Stage 2 plan worth knowing: `doctor --fix` re-applies your rig through the normal review screen instead of writing silently; the git protections are part of `apply` (opt out with `--no-git`); the sandbox is opt-in (`--sandbox`), sticky, and its enforcement is unverified until step 3.


## Stage 3: what the owner must do (S3-M10)

Built (plan: `docs/stage-3-plan.md`): target registry with per-target merge/projection/plan/state/lock; Codex, Gemini CLI, Cursor and Claude Desktop adapters on a shared toolkit; per-target base-secure mapping with honest "enforced / partly / instructions only" wording on every plan screen; `rigfile init --from <target>` capture with round-trip tests; Windows platform (user-only ACLs, `LockFileEx`, `.cmd` shim launching, PowerShell rules, Git-for-Windows hook paths, reserved-name check, CRLF/LF handling); WSL detection with cross-boundary denies; CI matrix with `windows-latest`; multi-target container E2E (Ubuntu and Fedora both pass locally).

**Not verified by anyone yet:** every line of Windows code has been compiled, vetted and unit-tested through injected environments, but not run on Windows; no adapter has been loaded by the real vendor tool. Both are the checklist in `docs/stage-3-owner-checks.md`:

1. Push (`git push -u origin stage-2 stage-3`), read the CI jobs, send me the `windows-latest` log if red.
2. Windows 11 clean-VM procedure (section 2 there).
3. Live vendor checks 1-7 (section 5), and the WSL glob check (section 4).
4. Finish the Stage 2 checklist above, then merge `stage-2` and `stage-3` into `main`.

Deviations worth knowing: Windows CI runs without `-race` and without the scanner timing gate; `init --from` cannot tell whole-file items Rigfile wrote (skills, agents) from yours unless `state.json` owns them, so run it before the first apply; Gemini CLI hooks/permissions and Cursor rules/hooks are not written at all (contracts unverified; the plan screen says so).


## Stage 3 result (merged to `main`, 2026-09-26)

Merge commit `10d820f`. CI: `stage-2` and `stage-3` green on all jobs. The first native Windows run found only test-side path assumptions plus one real bug (capture did not replace a forward-slash Windows home path in hook commands); all fixed. Evidence that Windows code runs natively: `internal/platform` (ACL `WritePrivate`/`IsPrivateFile`), `internal/execshim` (`.cmd` shim, argument-injection test) and `internal/secrets` (`LockFileEx`) tests pass on `windows-latest`. Still not run by anyone: real Credential Manager, real vendor tools loading the adapters' output, WSL, a clean Windows 11 VM (`docs/stage-3-owner-checks.md`).

Recorded in this session: the generated `docs/red-team.md` no longer prints per-OS rule counts (it made CI on Linux see the file as stale); the `redteam` doc and tests must stay OS-neutral. Local pushes by Claude are blocked by a user-level deny on `git push`, so pushes and merges to shared branches are the owner's.

## Stage 4: built, awaiting owner steps

Plan of record: `docs/stage-4-plan.md`; spec and threat model: `docs/sharing.md`. New commands: `pull`, `update`, `publish`, `logins`, `self-update`, `verify-signature`. New packages: `internal/{source,publish,tui,login,minisign,selfupdate}`, `tools/release`, `scripts/install.{sh,ps1}`, `.github/workflows/release.yml`, `e2e/install*.sh`.

What is proven: unit and CLI tests for every step; container E2E (Ubuntu and Fedora) of publish, pull, update and rollback; an installer E2E in which the real `minisign` signs a release and `install.sh` and `rigfile verify-signature` both accept it and refuse tampering, a wrong key, a replayed signature and a missing signature; reproducible release builds; wheel installed offline with pip and run; `node --check` on the npm scripts.

What is not: nothing here has run on real GitHub or GitLab, on Windows (PowerShell installer, native tests: first CI run), or with real people. No OAuth provider is registered. macOS notarization, Windows signing, rpm and an apt repository are not done. All of it, plus the LICENSE and package-name decisions and the signing key, is in `docs/stage-4-owner-checks.md`.

Say "go" for Stage 5 (registry website) only after the Stage 4 exit criteria (5+ external users) are met or you decide to defer them.


## Stage 5: built, awaiting owner steps

Plan of record: `docs/stage-5-plan.md`; spec and threat model: `docs/registry.md`; my security self-review: `docs/registry-security.md`. New: `internal/registry` (+ `blob`, `dbtest`), `internal/regclient`, `cmd/rigfile-registry`, `Dockerfile.registry`, `deploy/docker-compose.yml`, `e2e/registry.sh`, CLI commands `login`, `logout`, `whoami`, `publish --to-registry`, `pull owner/name`, registry-backed `from:` layers.

Design call worth knowing: one Go service (same scanner and validation code as the CLI) rather than Next.js + a separate API; pages are server-rendered with no script at all (CSP `default-src 'none'`).

Proven: store, API, auth flows, scan worker, pages (including hostile-content tests) and the CLI end to end against a real Postgres (`scripts/registry-test.sh`; CI job `registry (postgres)`); container E2E with the real image and two simulated machines; `govulncheck` clean.

Not proven: real GitHub sign-in, a real S3/R2 bucket, a real deployment, Windows/macOS runs of the new tests (they skip without a database), legal review, external security review, real users. See `docs/stage-5-owner-checks.md`.

Incident during the build: my first host-side E2E script ran the real CLI and triggered a macOS Keychain dialog on the owner's Mac (nothing was stored). Fixed with `RIGFILE_SECRETS_BACKEND=file`, which every host script must set.


## Stage 6: built, awaiting owner steps

Plan of record: `docs/stage-6-plan.md`; spec: `docs/trust.md` (§10 lists differences as built); runbook: `docs/incident-response.md`; policy: `SECURITY.md`; metrics: `docs/analysis-metrics.md`. New: `internal/{analyze,similar,sigverify,pkgcheck}`, registry migration `0002_trust.sql`, held-version queue and `/admin`, verified publishers, trust facts API and pull-screen section, Sigstore signature verification (registry and CLI), popular-rig policy, OSV lookups, publishing pause and token revocation.

Proven: unit and CLI tests for every part; registry tests against a real Postgres; a real public-good Sigstore bundle verified through the JSON path; analysis measured at 100% recall / 0 false positives **on a self-written corpus** (which says little about real attackers).

Not proven: the live Sigstore root fetch, OSV's live API, real signatures made in GitHub Actions, the rules against real-world rigs, the runbook under pressure, external review. See `docs/stage-6-owner-checks.md`.

## Stage 7: built, awaiting owner steps

Plan of record: `docs/stage-7-plan.md`; spec: `docs/rigd.md` (§7 records what was built); results: `docs/red-team-broker.md`; owner steps: `docs/stage-7-owner-checks.md`. New: `internal/rigd` (in-memory CA, host patterns, surrogates and sessions, intercepting CONNECT proxy, audit log, broker API and client, service files per OS), `rigfile broker run|status|enable|disable|exclude|include|install|uninstall|start|stop`, `rigfile exec` Level 2 (`--server`, `--allow`, `--bind`), adapters that write those flags from `network.allow` and `secrets.<ref>.hosts`, a per-server level in `rigfile doctor`.

Proven: the proxy and broker against local TLS servers; a real malicious child process against the real broker (29 attempts: everything blocked or reduced to a surrogate, one documented exception); `exec` with real child processes; a detached background broker end to end; service files as goldens and installs through a fake activator with rollback.

Not proven: a real launchd/systemd/scheduled-task install, real MCP servers and vendor APIs, Node/Python/Go clients against the CA variables, and one real gap: **a compromised child runs as you and can read the broker token** (`docs/red-team-broker.md`, last row). See `docs/stage-7-owner-checks.md`.

## Stage 8: first slice built, awaiting owner steps

Plan of record: `docs/stage-8-plan.md`; owner steps: `docs/stage-8-owner-checks.md`. Built: **version diffs** (`internal/rigdiff`, `rigfile changes`, the review banner in `rigfile update`, registry API and page; `docs/diffs.md`), **forks and use-as-base** (`rigfile fork`, derived rigs on the registry; `docs/forks.md`), **collections** (`docs/collections.md`), **organisations** with membership as the access control (`docs/orgs.md`, migrations 0003 and 0004, `rigfile org`, admin `disable-org`), and **`rigfile ui`**, a guarded local checklist page (`docs/local-ui.md`). Researched, not built: Windsurf, Zed, VS Code Copilot targets (`docs/targets/`).

Proven: unit and CLI tests for every part; the registry parts against a real Postgres, including an organisation red-team over every read route and a concurrent-namespace race test.

Not proven: real browsers, a real registry deployment, real teams, the editors' file formats. The riskiest change is the pair of visibility fragments in `internal/registry/store_rigs.go`, which every read now goes through.

**Stage 8 addendum (branch `stage-8b`, from `stage-8`):** the GitHub Copilot in VS Code adapter is built, project-scoped (`docs/targets/vscode-copilot.md`). Matrix regenerated.

**Broker-token gap (branch `stage-8b`):** closed as far as software on one account can: session requests carry no hosts, and the broker builds sessions from the policy `rigfile apply` writes into `state.json` (`docs/rigd.md` §3a). The red team is now 32 attempts with no "evades" row; the residue is "borrow another approved server's session" (spend its key at its own host), documented as not stopped.

**Private sync (branch `stage-8c`, from `stage-8b`):** built (`docs/private-sync.md` §7): `internal/vault` and `rigfile sync`, end-to-end encrypted, signed roster chain, rollback protection, directory or git transport, red-teamed against a hostile storage. Also: a `private:` path that the rig ships is now a manifest error.
