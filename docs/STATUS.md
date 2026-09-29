# Rigfile status

**Current state (2026-09-28): all build stages (1-8, plus 3b local models) are merged to `main`. Git-based sharing is now the primary, documented way to share a rig; the registry is a real, tested, optional feature that is currently not deployed (torn down 2026-09-28; see `docs/registry.md` §8 to redeploy it). The GitHub repository is still private, by owner choice. Everything genuinely still open — for the owner, not for further building — is in `docs/owner-checklist.md`, which replaced the old per-stage owner-check documents.**
Last updated: 2026-09-28

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
| 2026-09-25 | **Go confirmed** after the spike (go/no-go: go) | ADR 0001 |
| 2026-09-25 | **User-scope MCP written via `claude mcp add-json --scope user`**, not by editing `~/.claude.json` | ADR 0002 |
| 2026-09-25 | Owner allows a **read-only `rigfile init` capture of their real `~/.claude`** into a scratch dir to build the first sanitized fixture; nothing enters the repo before the owner reviews it | owner, 2026-09-25 |

## Still open (not blocking the Stage 1 spike)

- §17 Q1 open source / licence: **no LICENSE file yet** (default = all rights reserved). Needed before the repo is public or CI is added. The repo already has a GitHub `origin` remote; nothing has been pushed from this environment.
- Q2 business model, Q5 private memory sync (default: defer to Stage 8), Q6 domains/names (schema `$id` is a placeholder), Q7 targets after Claude Code (Stage 3), Q9 WSL, Q10 code-signing, Q11 local-model scope.
- Plan corrections found in Stage 0 that the owner has not yet folded into `RIGFILE_PLAN.md` (see the "Summary of plan corrections" tables in `docs/targets/*.md` and `docs/platforms.md` §7): notably §9.5 local-model wiring, Codex hook trust, Codex/Claude Code permission syntax, `${CONFIG_DIR}`, credential files in the deny list.
- Not done in Stage 0: Cursor / Gemini CLI / Claude Desktop target docs (Stage 3); sanitized fixture of the owner's own setup (needs an export from the owner).

## Stage 1 spike — result (branch `stage-1-spike`, not merged)

Built and tested 2026-09-25: manifest validation, marker-splice editing, JSON layout-preserving edits, platform layer, Claude Code permissions adapter with backup-first writes, secrets (keychain + age fallback), exec shim, PreToolUse guard, CLI. 179 tests, race-clean, no skips on macOS. Hook ≈ 5 ms/call; release binaries 4.7–5.8 MB on darwin/arm64, linux/amd64, linux/arm64, windows/amd64 (the full spike report was in the now-removed `docs/spike-report.md`; ADR 0001 has the go/no-go decision).
**Owner confirmed go.** Still open from the spike: real-Keychain and real-Linux checks, Linux/Windows latency, file-store lock.

## Stage 1 — build result (branch `stage-1`)

Milestones M1-M9 (plan §12) are all done: manifest/merge/layers, lock/state/rollback, Claude Code adapter (all seven categories), CLI (`init validate plan apply diff rollback lock doctor secrets exec hook`), tools installer, secrets completion, CI workflow, container E2E (Ubuntu + Fedora pass), and the owner's rig fixture (`testdata/fixtures/sample-rig/`). `go test -race ./...` is green on macOS; Windows cross-builds and vets.

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

## Pre-publish history audit (`main`, 2026-09-28)

Before considering the repository public: `gitleaks git --log-opts="--all"` over every branch, 158 commits, no secrets found. All commit authors use the GitHub no-reply address (no real email exposed). Two remaining privacy items, owner-approved: test data used the owner's real first name and a real machine hostname, and two made-up gmail addresses that could belong to real strangers — replaced throughout with a made-up user, `ada`, and addresses on the owner's own domain (old commits keep the old values; history was not rewritten). `testdata/fixtures/jia-rig`, a sanitised capture of the owner's real Claude Code setup, was replaced with a synthetic equivalent, `testdata/fixtures/sample-rig` (same test coverage: a skill with a script, a command, an unpinned MCP server, `secret://` refs), and `TestJiaRigFixture` became `TestSampleRigFixture`. Also deleted: the pre-Stage-0 Python scaffold and a stray empty `main` file (see below).

**Correction (2026-09-30), from a full pre-launch audit:** the scrub above only ever landed on `main`. `main` itself never merged the real Go implementation (it stayed Stage-0 planning docs), so it looked clean in isolation — but `stage-1` through `stage-8d`, `integration` and `fix-ci-integration` predate the scrub and still carried the real first name, hostname, fake-but-real-domain email addresses, and the real `testdata/fixtures/jia-rig/` capture at their tips. Confirmed live on `origin` at first (via a stale local cache, corrected by `git fetch --prune`) — **then confirmed, by a live `git ls-remote`/`gh api` check, already deleted from `origin`**: only `main` exists on the remote now. Whoever deleted them, this specific risk is closed; only local copies remain, which were never public. `RIGFILE_PLAN.md` also still used the owner's real first name throughout, on every branch including `main` — scrubbed today, see below.

## Notes

- ~~A Python scaffold (`pyproject.toml`, `src/rigfile`, `tests/`) that predated Stage 0~~ — **deleted 2026-09-27** (owner decision) with a real `README.md`; recoverable from history (`42bd6bc`).
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

Built: target registry with per-target merge/projection/plan/state/lock; Codex, Gemini CLI, Cursor and Claude Desktop adapters on a shared toolkit; per-target base-secure mapping with honest "enforced / partly / instructions only" wording on every plan screen; `rigfile init --from <target>` capture with round-trip tests; Windows platform (user-only ACLs, `LockFileEx`, `.cmd` shim launching, PowerShell rules, Git-for-Windows hook paths, reserved-name check, CRLF/LF handling); WSL detection with cross-boundary denies; CI matrix with `windows-latest`; multi-target container E2E (Ubuntu and Fedora both pass locally).

**Not verified by anyone yet:** every line of Windows code has been compiled, vetted and unit-tested through injected environments, but not run on Windows; no adapter has been loaded by the real vendor tool. Both are now in `docs/owner-checklist.md` §3 "Real-machine verification":

1. Push (`git push -u origin stage-2 stage-3`), read the CI jobs, send me the `windows-latest` log if red.
2. Windows 11 clean-VM procedure (section 2 there).
3. Live vendor checks 1-7 (section 5), and the WSL glob check (section 4).
4. Finish the Stage 2 checklist above, then merge `stage-2` and `stage-3` into `main`.

Deviations worth knowing: Windows CI runs without `-race` and without the scanner timing gate; `init --from` cannot tell whole-file items Rigfile wrote (skills, agents) from yours unless `state.json` owns them, so run it before the first apply; Gemini CLI hooks/permissions and Cursor rules/hooks are not written at all (contracts unverified; the plan screen says so).


## Stage 3 result (merged to `main`, 2026-09-26)

Merge commit `10d820f`. CI: `stage-2` and `stage-3` green on all jobs. The first native Windows run found only test-side path assumptions plus one real bug (capture did not replace a forward-slash Windows home path in hook commands); all fixed. Evidence that Windows code runs natively: `internal/platform` (ACL `WritePrivate`/`IsPrivateFile`), `internal/execshim` (`.cmd` shim, argument-injection test) and `internal/secrets` (`LockFileEx`) tests pass on `windows-latest`. Still not run by anyone: real Credential Manager, real vendor tools loading the adapters' output, WSL, a clean Windows 11 VM (`docs/owner-checklist.md` §3).

Recorded in this session: the generated `docs/red-team.md` no longer prints per-OS rule counts (it made CI on Linux see the file as stale); the `redteam` doc and tests must stay OS-neutral. Local pushes by Claude are blocked by a user-level deny on `git push`, so pushes and merges to shared branches are the owner's.

## Stage 4: built, awaiting owner steps

Spec and threat model: `docs/sharing.md`. New commands: `pull`, `update`, `publish`, `logins`, `self-update`, `verify-signature`. New packages: `internal/{source,publish,tui,login,minisign,selfupdate}`, `tools/release`, `scripts/install.{sh,ps1}`, `.github/workflows/release.yml`, `e2e/install*.sh`.

What is proven: unit and CLI tests for every step; container E2E (Ubuntu and Fedora) of publish, pull, update and rollback; an installer E2E in which the real `minisign` signs a release and `install.sh` and `rigfile verify-signature` both accept it and refuse tampering, a wrong key, a replayed signature and a missing signature; reproducible release builds; wheel installed offline with pip and run; `node --check` on the npm scripts.

What is not: nothing here has run on real GitHub or GitLab, on Windows (PowerShell installer, native tests: first CI run), or with real people. No OAuth provider is registered. macOS notarization, Windows signing, rpm and an apt repository are not done. All of it, plus the signing key, is in `docs/owner-checklist.md` §3.

Say "go" for Stage 5 (registry website) only after the Stage 4 exit criteria (5+ external users) are met or you decide to defer them.


## Stage 5: built, awaiting owner steps

Spec and threat model: `docs/registry.md`; my security self-review: `docs/registry-security.md`. New: `internal/registry` (+ `blob`, `dbtest`), `internal/regclient`, `cmd/rigfile-registry`, `Dockerfile.registry`, `deploy/docker-compose.yml`, `e2e/registry.sh`, CLI commands `login`, `logout`, `whoami`, `publish --to-registry`, `pull owner/name`, registry-backed `from:` layers.

Design call worth knowing: one Go service (same scanner and validation code as the CLI) rather than Next.js + a separate API; pages are server-rendered with no script at all (CSP `default-src 'none'`).

Proven: store, API, auth flows, scan worker, pages (including hostile-content tests) and the CLI end to end against a real Postgres (`scripts/registry-test.sh`; CI job `registry (postgres)`); container E2E with the real image and two simulated machines; `govulncheck` clean.

~~Not proven: real GitHub sign-in, a real S3/R2 bucket, a real deployment~~ — **done 2026-09-27**, see "Registry: live deployment" below. Still not proven: Windows/macOS runs of the new tests (they skip without a database), legal review, external security review, real users other than the owner. See `docs/owner-checklist.md`.

Incident during the build: my first host-side E2E script ran the real CLI and triggered a macOS Keychain dialog on the owner's Mac (nothing was stored). Fixed with `RIGFILE_SECRETS_BACKEND=file`, which every host script must set.

**Website and documentation (`main`, 2026-09-27):** the registry is now a product site, not just app pages. New `/docs` section (`internal/registry/pages_docs.go`, `web/docs/*.md`, rendered through the same goldmark + bluemonday path as README files): Getting started, Concepts, Writing a rig, Secrets, Publishing, Pulling safely, Forks/collections/organisations, Local models, Sync, Manifest reference, CLI reference, Supported tools (generated at request time from `internal/targets` capability files, so it cannot drift), Security model and limits, FAQ with the real errors seen in production. Examples use the serving registry's own address (`%REGISTRY%`). Also: landing page (value prop, how it works, features, supported tools), header nav + script-free mobile menu, footer, favicon, meta/OpenGraph tags, designed 404, Explore page, rig-page section nav and click-to-select commands. Still no JavaScript; CSP unchanged. Drift guards: every nav page has a file and vice versa, internal doc links resolve, the CLI reference covers every command in `rigfile --help` (`cmd/rigfile/docs_cli_test.go`), no template uses inline styles. The documented example manifest validates with the real CLI. Verified locally: 31 internal links, 0 broken; real 390px mobile emulation (Playwright) shows no horizontal overflow. Correction to an earlier note: the pin check blocks **every** `publish` (registry or `--to-git`), and `rigfile init` names rigs `local/my-rig` by default, which the registry refuses for non-admins (`owner != login`); the Quickstart and docs now say `init --name <login>/...`. Owner actions: make `rigfile/rigfile` public (the site links to it and the install step clones it), then `fly deploy`. The generated README now names the registry (with a placeholder the registry fills in) as well as git; the registry also fixes already-published READMEs at display time.

**UI redesign (`main`, 2026-09-27):** the registry's pages were plain unstyled HTML. Rewrote `internal/registry/web/static/style.css` (design tokens, badges, a GitHub-style repo header, diff highlighting, avatars) and the templates that needed structural changes to use it; no script anywhere (CSP stays `default-src 'none'`). Verified by actually building and running the registry locally (`deploy/docker-compose.yml` against a real Postgres), publishing a real rig with the real CLI, and screenshotting home/rig/profile/diff with headless Chrome — caught and fixed one real usability bug that way (a sidebar `<pre>` command was clipped) and one real pre-existing CSP bug unrelated to the redesign (`device.html`'s OTP code used an inline `style=` attribute, which `style-src 'self'` silently drops).

### Registry: down, teardown pending owner action (`main`, 2026-09-28)

The deployment below is no longer live: Fly's free trial ended (every `flyctl` command, including `apps destroy`,
now refuses with "trial has ended, please add a credit card"), and `/healthz` is unreachable. Strategic decision
the same day: git-based sharing (already fully built) is now the primary, documented way to share rigs; the
registry is optional. Owner chose full teardown over paying to keep it running; steps are owner-only (Fly and
Neon dashboards, R2 dashboard) since I never held R2 credentials and Fly blocks me even from deleting it. See
`docs/owner-checklist.md` "Strategic change" for the exact steps. The code (`internal/registry`,
`deploy/docker-compose.yml`) is untouched and still runs locally against a fresh Postgres/bucket if wanted again.

### Registry: live deployment (`main`, 2026-09-27) -- history; the deployment described below no longer runs

Deployed to production: **`https://rigfile.bytebuilderslab.app`** (Fly.io app `rigfile`, two `app` machines, `auto_stop_machines`; Neon Postgres, point-in-time restore up to 30 days; Cloudflare R2 for the blob bucket; DNS is a CNAME at the owner's registrar to the Fly `.fly.dev` hostname, per Fly's own guidance for a subdomain). `/healthz` and TLS both verified. GitHub OAuth App `Ov23licYmVqaKBqv1rNb` (org `rigfile`), secrets set via `fly secrets set` by the owner directly (never pasted to me).

Owner then ran the exit criterion end to end for real: `rigfile login`, `rigfile publish <dir> --to-registry` (private by default), the registry page. One real finding along the way: **the pin-check applies to every registry publish, private or public** — `publish.Prepared.Blocked()` runs before the `--public` branch in `cmd_publish.go`, so an unpinned `uvx`/`npx`/`pipx` package blocks even a private upload (rationale: a private rig can be flipped public later without a new upload, so the bar is enforced once, at upload time). Not a bug; documented here since the CLI's own message ("a public rig must pin what it runs") reads as public-only. Fixed by pinning the owner's own `alpaca-mcp-server` entry to the real current PyPI release. Publish → scan → registry page all confirmed working live.

~~Not yet run: a restore drill from the Neon backup, and a second real GitHub account pulling a published rig from a different machine~~ — **both done 2026-09-27.** The first second-account pass used the operator tool (`admin create-user`/`admin token`) rather than a second real GitHub login, but it created a genuine non-admin account — the owner's own rig had only ever gone through the admin bypass in `internal/registry/api.go` (`owner != u.Login && !u.IsAdmin`), since they are the sole admin. From a third, fully anonymous machine: the public test rig pulled successfully; the owner's private rig was correctly refused. The owner then repeated it for real with an actual second GitHub account (`rigfile-bot`): signed in separately, a throwaway public rig published under the owner's real login was visible with its content; `local/my-rig` (private) correctly showed no published version. Both confirmed. The Neon restore drill (point-in-time branch, queried directly, real rows came back, branch deleted) proves the mechanism; a written incident procedure (which timestamp, how to cut over `RIGFILE_REGISTRY_DATABASE_URL`, who decides) still doesn't exist.


## Stage 6: built, awaiting owner steps

Spec: `docs/trust.md` (§10 lists differences as built); runbook: `docs/incident-response.md`; policy: `SECURITY.md`; metrics: `docs/analysis-metrics.md`. New: `internal/{analyze,similar,sigverify,pkgcheck}`, registry migration `0002_trust.sql`, held-version queue and `/admin`, verified publishers, trust facts API and pull-screen section, Sigstore signature verification (registry and CLI), popular-rig policy, OSV lookups, publishing pause and token revocation.

Proven: unit and CLI tests for every part; registry tests against a real Postgres; a real public-good Sigstore bundle verified through the JSON path; analysis measured at 100% recall / 0 false positives **on a self-written corpus** (which says little about real attackers).

Not proven: the live Sigstore root fetch, OSV's live API, real signatures made in GitHub Actions, the rules against real-world rigs, the runbook under pressure, external review. See `docs/owner-checklist.md`.

## Stage 7: built, awaiting owner steps

Spec: `docs/rigd.md` (§7 records what was built); results: `docs/red-team-broker.md`; owner steps: `docs/owner-checklist.md`. New: `internal/rigd` (in-memory CA, host patterns, surrogates and sessions, intercepting CONNECT proxy, audit log, broker API and client, service files per OS), `rigfile broker run|status|enable|disable|exclude|include|install|uninstall|start|stop`, `rigfile exec` Level 2 (`--server`, `--allow`, `--bind`), adapters that write those flags from `network.allow` and `secrets.<ref>.hosts`, a per-server level in `rigfile doctor`.

Proven: the proxy and broker against local TLS servers; a real malicious child process against the real broker (29 attempts: everything blocked or reduced to a surrogate, one documented exception); `exec` with real child processes; a detached background broker end to end; service files as goldens and installs through a fake activator with rollback.

Not proven: a real launchd/systemd/scheduled-task install, real MCP servers and vendor APIs, Node/Python/Go clients against the CA variables, and one real gap: **a compromised child runs as you and can read the broker token** (`docs/red-team-broker.md`, last row; narrowed since by `rigfile exec --confine`, see `docs/owner-checklist.md` §4). See `docs/owner-checklist.md`.

## Stage 8: first slice built, awaiting owner steps

Owner steps: `docs/owner-checklist.md`. Built: **version diffs** (`internal/rigdiff`, `rigfile changes`, the review banner in `rigfile update`, registry API and page; `docs/diffs.md`), **forks and use-as-base** (`rigfile fork`, derived rigs on the registry; `docs/forks.md`), **collections** (`docs/collections.md`), **organisations** with membership as the access control (`docs/orgs.md`, migrations 0003 and 0004, `rigfile org`, admin `disable-org`), and **`rigfile ui`**, a guarded local checklist page (`docs/local-ui.md`). Researched, not built at the time: Windsurf, Zed, VS Code Copilot targets (`docs/targets/`). VS Code Copilot and (as of the addenda below) Devin and Zed have since been built.

**Devin adapter (`main`, 2026-09-27, owner decision):** built. The product is Devin now, not Windsurf — the owner confirmed on their own machine (added a server through the app's UI, `~/.config/devin/mcp_config.json` with a top-level `mcpServers` key). `internal/adapters/devin`: stdio MCP servers only, user scope, `rigfile exec` wrapping; remote servers skipped (the vendor docs disagree on the field name), instructions/rules not covered by any documentation found. Target `devin`, capability file, goldens for all three OSes, round-trip capture test (`docs/targets/devin.md`).

**Zed adapter (`main`, 2026-09-27):** built. `internal/adapters/zed`: `context_servers` in `settings.json`, both stdio (through `rigfile exec`) and remote (Zed's docs are unambiguous about `url`/`headers`, unlike Devin's), written through `internal/jsonedit` so a real, comment-heavy `settings.json` keeps its comments outside the edited member. Added `jsonedit.Mask` (comments/trailing commas → spaces, same byte offsets) so capture can `encoding/json.Unmarshal` a real file instead of failing on it. Target `zed`, capability file, goldens for all three OSes, comment-preservation/remote/round-trip-capture tests (`docs/targets/zed.md`). Windows path still unconfirmed by a real run.

Proven: unit and CLI tests for every part; the registry parts against a real Postgres, including an organisation red-team over every read route and a concurrent-namespace race test.

Not proven: real browsers, a real registry deployment, real teams, the editors' file formats. The riskiest change is the pair of visibility fragments in `internal/registry/store_rigs.go`, which every read now goes through.

**Stage 8 addendum (branch `stage-8b`, from `stage-8`):** the GitHub Copilot in VS Code adapter is built, project-scoped (`docs/targets/vscode-copilot.md`). Matrix regenerated.

**Broker-token gap (branch `stage-8b`):** closed as far as software on one account can: session requests carry no hosts, and the broker builds sessions from the policy `rigfile apply` writes into `state.json` (`docs/rigd.md` §3a). The red team is now 32 attempts with no "evades" row; the residue is "borrow another approved server's session" (spend its key at its own host), documented as not stopped.

**Broker session isolation (`main`, 2026-09-27, owner decision):** `rigfile exec --confine` narrows the residue above: a server launched with `--confine` under Level 2 is network-sandboxed (macOS `sandbox-exec`, Linux Landlock via a hidden re-exec helper) so it can reach the broker's proxy but not its control API — closing the case where the borrowing process is a server Rigfile itself launched, which is the broker's actual threat model (`docs/rigd.md` §1). Verified live on macOS (`TestExecConfineBlocksTheBrokerControlAPI`); **UNVERIFIED on Linux** (needs Landlock ABI 4 / kernel 6.7+, absent on this project's own dev/CI machines, so only the fail-closed path is exercised there). Opt-in per launch; not wired into the manifest yet. A wholly separate process that never went through `rigfile exec` is still not covered (`docs/rigd.md` §8).

**Private sync (branch `stage-8c`, from `stage-8b`):** built (`docs/private-sync.md` §7): `internal/vault` and `rigfile sync`, end-to-end encrypted, signed roster chain, rollback protection, directory or git transport, red-teamed against a hostile storage. Also: a `private:` path that the rig ships is now a manifest error.
## Stage 3b: local models, built, awaiting owner steps

Spec as built: `docs/models.md`; owner steps: `docs/owner-checklist.md`. Branch `stage-3b` (created from `stage-8`, so it contains Stage 8's commits: merge `stage-8` first; not pushed). Built: hardware detection, the model catalog (Ollama engine version and digest verified 2026-09-26), variant selection and safety validation, the MODELS plan section, pinned hash-verified Hugging Face downloads, Ollama pulls with digest check, a generic per-user service generator (`internal/svc`, also used by `rigd`), `rigfile models list|pull|status|serve|url|run|rm`, `apply --models now|later|skip`, `doctor` checks (server, loopback only, chat, tool-call smoke test), and capture of a running Ollama by `rigfile init`.

**Deviation from the plan:** the exit criterion (Codex and Claude Code using the mlx-lm reference setup) is not reachable without a bridge nobody verified, so it is re-scoped: the reference setup is reproduced and served, and Codex and Claude Code (experimental) are wired only through Ollama. No gateway, no routing translation, no global agent configuration is written.

Not proven: real downloads, real Apple Silicon, real service managers, real agents against a local model. See the owner checks.

**JSONC (branch `stage-8d`, from `stage-8c`):** `internal/jsonedit` edits JSON with comments and trailing commas by masking; the Copilot adapter uses it (a commented `mcp.json` is edited, comments kept).

## Real-machine verification (`main`, 2026-09-27)

Stage 1's own exit criterion ("on a clean macOS VM... verified by doctor and a manual smoke test") was never run until now. First real run, on a throwaway Tart VM (`ghcr.io/cirruslabs/macos-sequoia-base`, real Apple Virtualization.framework, real Apple Silicon, no emulation): `e2e/run-macos.sh`, `e2e/macos-scenario.sh`, `e2e/adr0002-smoke.sh` (owner-run only; no macOS CI runner). Result: **MACOS E2E OK** — the whole core loop (validate/plan/apply/idempotent/secrets/exec/doctor/diff/rollback) on real macOS, with the **real OS Keychain** (`doctor` said `macos-keychain`, confirmed the value is actually in `security find-generic-password`, not the encrypted-file fallback container tests are stuck with). Also ran the real `claude` CLI (2.1.283) for the first time: every one of ADR 0002's explicitly UNVERIFIED assumptions about `claude mcp get/add-json/remove --scope user` holds (`docs/adr/0002-mcp-user-scope-write-path.md`, updated). One real, previously-unknown fact surfaced and documented there: `claude mcp get` initialises and backs up `~/.claude.json` on any invocation, a vendor side effect `rigfile plan` cannot avoid while it checks MCP status — not a Rigfile bug, and `e2e/macos-scenario.sh`'s "plan writes nothing" check was corrected to mean Rigfile's own managed content, not the vendor's file.

Also broadened the container e2e: `internal/adapters/devin` and `internal/adapters/zed` (both built today) are now exercised in `e2e/scenario.sh`'s multi-target section; an Arch Linux (pacman) container was added (`e2e/Dockerfile.arch`), CI-only (`e2e/run.sh`'s own default stays `ubuntu fedora`) since archlinux has no arm64 image and running amd64-under-QEMU on this dev machine crashed non-deterministically (a QEMU bug, not Rigfile's — see `e2e/README.md`).

**Local models, hardware detection (`main`, 2026-09-27):** `rigfile models list` on a real 4 GB Tart VM correctly read the real memory via `sysctl hw.memsize`, correctly excluded both mlx catalog variants (16 GB / 32 GB needed), and correctly fell back to the Ollama `qwen3:8b` entry — the documented cross-hardware behaviour, run for real for the first time. The actual weight download and a loaded model answering a prompt are still not run: this dev host has only 16 GB of RAM total, so a VM meeting the mlx variant's own 16 GB+ requirement is not safe to allocate here.

Still not run for real at the time: Windows, WSL, a real launchd/systemd/Windows-task service install (Stage 7), and the local-models weight download/inference above (needs more host RAM, or the owner's own machine).

## Windows real-machine verification (`main`, 2026-09-29)

First real Windows run, on a Windows 11 24H2 ARM64 VM (build 26100.4349, UTM/QEMU on the owner's Apple Silicon Mac, free — the licensing/account blocker above turned out not to be one: UTM's ARM64 Windows fetch and a standard local account both worked with no Microsoft account or paid license needed). Owner drove the VM directly (installed Go/Git/`gh` via winget, ran commands, pasted output back); this also covers most of the separate "Windows on ARM" real-hardware gap, not just the general Windows one.

Verified: install from source, `validate`/`plan --no-git` (correct manifest, hooks and permission listing for the `windows` target), `secrets set` → confirmed via `doctor` as `windows-credential-manager` (the Credential Manager GUI itself wasn't independently cross-checked in the same session), `icacls` on the install dir correctly restricted to the user + SYSTEM/Administrators (nothing for Users/Everyone), `apply`, `doctor`, `diff` (no drift), `rollback --force`, the full git-hook secret-blocking path through Git for Windows (a normal commit succeeds, a commit adding a real-looking fake-value secret is blocked by the pre-commit hook, `git commit --no-verify` on the same content is separately blocked by the reference-transaction hook backstop), five concurrent `rigfile secrets set` calls (PowerShell `Start-Job`) losing no update, and `rigfile exec -- npx ...` correctly resolving and running through the `.cmd` shim.

Two real, previously-unknown facts surfaced and fixed:
1. The secret scanner (`internal/scan`) skipped any file PowerShell's `>`/`Out-File` wrote, because their default UTF-16LE-with-BOM encoding puts a null byte next to every ASCII character — exactly `isBinary`'s signal to skip a file as non-text, so a real secret in such a file was never scanned at all. `decodeIfUTF16` (`internal/scan/util.go`) now detects a UTF-16 BOM (either endianness) and transcodes to UTF-8 before the binary check; content with no BOM is unaffected. `TestUTF16WithBOMIsDecodedNotSkippedAsBinary` covers both endiannesses and that genuinely binary content still gets skipped.
2. **Security-sensitive:** `rigfile exec -- npx --version "a & calc"` launched Calculator — a real cmd.exe argument-injection vulnerability, proving this project's prior assumption ("Go runs `.cmd` through cmd.exe with its own escaping, refusing arguments it cannot escape") was never true; Go's own `os/exec` docs say the opposite (handling cmd.exe's separate command-line parsing is the caller's job). Fixed: `internal/execshim` now refuses (rather than runs) an argument containing a cmd.exe metacharacter (`&|<>^()%"` or a control character — the set Microsoft's own `cmd` reference documents, verified live) when the resolved program is a `.bat`/`.cmd` file. Re-verified with the identical argument on the same real hardware: refused with a clear error, Calculator does not open; the normal case is unaffected. `TestScriptShimRefusesAnArgumentCmdExeWouldReinterpret` and unit tests run on every OS, not just under `GOOS=windows`.

A third apparent finding was a false lead, corrected the same day: `rigfile apply`'s `[a]pply [q]uit` prompt appeared not to register keypresses in one specific legacy "Windows PowerShell" console window (Ctrl+C still worked). A diagnostic subcommand confirmed stdin bytes actually arrive correctly, and `apply` itself then worked cleanly, twice, in freshly opened windows of the same console host — the original failure was most likely that one window's leftover state from the VM force-stop/network-mode troubleshooting happening around the same time, not a real Rigfile or Windows-console bug. Detail in `docs/platforms.md` §8, so the false lead isn't rediscovered.
