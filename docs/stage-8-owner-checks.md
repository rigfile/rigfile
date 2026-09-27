# Stage 8 owner checks (S8-M7)

Stage 8 is a bucket, so it has no single exit criterion. This slice is built and tested against fakes, a local Postgres and real child processes; nothing below has been run against a real registry deployment, a real browser session on your machine, or the third-party editors.

## 1. Push and read CI

`git push -u origin stage-8`. New things that matter on Windows and Linux: `internal/rigdiff` (paths and line endings), `TestForkCopiesARigAsYourOwn` (permissions, skipped on Windows), `internal/localui` (loopback server, cookie jar), `TestUIShowsThePlanStoresDeclaredSecretsAndApplies` (a real `rigfile ui` on the file secret backend), and the registry job (migrations `0003` and `0004`, including the namespace triggers and the concurrency test). Send me any red log, or I will read it myself once you have pushed.

## 2. Migrations on a real database

Migrations `0003_collections.sql` and `0004_orgs.sql` run automatically at startup. `0004` adds a column to `rigs`, two tables and two triggers, and **changes the two visibility fragments every query uses**. Before deploying:

- take a database backup;
- run the registry against a copy of production first and open a private rig, a public rig and a pending version as three different accounts;
- check `SELECT count(*) FROM users u JOIN orgs o ON o.login = u.login` is 0 (it is by construction; a non-zero result would mean a hand-edited table).

## 3. Decisions

| Decision | Recommendation | Where it lands |
|---|---|---|
| ~~**Who may create an organisation.**~~ Confirmed 2026-09-26: keep as built — any signed-in user, capped at 10, members fully trusted to publish with no approval step. `rigfile-registry admin disable-org` is the brake | revisit once real, contested names start to matter | `store_orgs.go` |
| **Org members are fully trusted to publish.** No approval step, no per-rig roles | accept for a team registry; if you want two-person review, say so and I will design an approval queue | `docs/orgs.md` |
| **Are org rigs allowed to be public?** Yes, as built (same review rules as personal rigs) | keep | `SetVisibility` |
| **`rigfile ui` applies without a terminal prompt** after a checkbox | acceptable: same code path, backups and rollback; it refuses a rig whose lockfile no longer matches | `docs/local-ui.md` |
| **Windsurf, Zed, VS Code Copilot** | your call on scope for Copilot (project files recommended); read the "what settles it" section of each file in `docs/targets/` | `docs/targets/` |

## 4. Live checks I could not run

| # | Check | How |
|---|---|---|
| 1 | `rigfile ui` in a real browser on macOS, Linux and Windows: cookie landing, storing a secret into the real OS keychain, Apply | `rigfile ui <a rig> ` on each OS; use a throwaway secret; **check that the keychain prompt (macOS) names rigfile**, and that the address bar shows no token after landing |
| 2 | Diff and organisation pages in a real browser (layout, escaping of a hostile file) | a staging registry; publish a version whose instruction file contains `<script>` and confirm it appears as text |
| 3 | `rigfile update` against a real registry shows what changed | publish `1.0.0` then `1.1.0` with an added MCP server; `rigfile pull`, then `rigfile update --plan-only` |
| 4 | Editors' settings paths | the "what settles it" section of `docs/targets/devin.md (formerly windsurf.md)`, `zed.md`, `vscode-copilot.md` |
| 5 | Nothing in this slice is exercised by more than a handful of real users. Organisations and collections need real teams before their limits (50 collections, 100 items, 200 members) can be judged | watch after launch |

## 5. Not built, and what each would need

- **Private memory sync between your own machines (end-to-end encrypted).** A security design of its own: how a second machine obtains the key without the server seeing it, what is synced, conflict rules, a transport. **The design is written: `docs/private-sync.md`** (bring-your-own transport, age multi-recipient encryption, device enrolment with a fingerprint check, revocation, rollback detection). It lists six decisions for you; nothing is built until you make them.
- **Hosted cloud rig.** The plan defers it until the local product has traction.
- **More Linux distros and ARM (Raspberry Pi, Windows on ARM).** The release tool already builds arm64; it needs clean-machine runs I cannot do. List for you: `rigfile self-update`, `rigfile apply` and `rigfile broker run` on Raspberry Pi OS (arm64), Alpine (musl), and Windows 11 on ARM.
- **Windsurf, Zed, VS Code Copilot adapters:** researched, blocked on paths (above).
- ~~Web forms for collections and organisations~~ **Built afterwards** (`docs/collections.md`, `docs/orgs.md` §Web).

## 6. External security review

Add the items in `docs/registry-security.md` §7 to the review scope. The riskiest change of the stage is the visibility fragments in `store_rigs.go`; ask the reviewer to try to read a private organisation rig from every route.
