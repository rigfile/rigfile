# Stage 8 plan of record: growth features (first slice)

**Source (RIGFILE_PLAN.md §12):** "Forks and 'use as base' UI, version diffs, collections, org/team private registries, local web checklist UI, more Linux distros and ARM, more targets (Windsurf, Copilot, Zed, ...), private memory sync between the owner's own machines (end-to-end encrypted), optional hosted cloud rig."

Stage 8 has no single exit criterion: it is a bucket. Started 2026-09-26 on branch `stage-8`, created from `main` (Stages 1-7 merged). This document says which items make up the first slice, in what order, and which I am deliberately not building.

## What I build, and why in this order

| # | Item | Why now | Depends on |
|---|---|---|---|
| S8-M1 | **Version diffs** | The most useful trust feature on the list: before you `update`, see exactly what a new version adds (a new MCP server, a hook, a permission change, a new secret) instead of trusting the version number. One shared diff engine serves the registry page, the API and the CLI | nothing |
| S8-M2 | **Forks and "use as base"** | The manifest already has `from:` layering; the registry only needs to *record and show* it (who builds on this rig) and the CLI needs `rigfile fork` to start a rig on top of another | S8-M1 (fork page links to diffs) |
| S8-M3 | **Collections** | Curated lists of rigs (a page and an API). Small, self-contained, reuses the visibility predicate | nothing |
| S8-M4 | **Org and team registries** | A namespace several people can publish under, with private rigs visible to members. It touches the visibility predicate and the namespace rules, so it gets the heaviest tests | nothing, but do it after the simpler registry items so the predicate work is not rushed |
| S8-M5 | **Local web checklist UI** (`rigfile ui`) | A loopback page showing the plan and the checklist of secrets and logins still needed, for people who do not live in a terminal | existing `session.Prepared` |
| S8-M6 | **More targets** (Windsurf, GitHub Copilot in VS Code, Zed) | Reach. Each needs its formats verified against official docs first (working agreement 2), so this comes last and is the most likely to end with **UNVERIFIED** rows | vendor docs |
| S8-M7 | **Owner gate** | `docs/stage-8-owner-checks.md` | all |

## Not building (and why)

- **Private memory sync between the owner's machines.** End-to-end encrypted sync needs key management (how a second machine gets the key without the server seeing it), a transport and conflict rules. That is a security design in its own right; it deserves its own spec and your decisions before code. I will write that spec if you ask.
- **Optional hosted cloud rig.** The plan itself says "out of scope until the local product has traction".
- **More Linux distros and ARM (Raspberry Pi, Windows on ARM).** Mostly release matrix and clean-VM verification: the code is already portable and the release tool already builds arm64. It needs machines I do not have. It is in the owner checks as a list to try.

## Design calls

1. **One diff engine, `internal/rigdiff`.** It takes two rig directories (or two manifests) and returns a structured change list: manifest-level entries (MCP servers, hooks, permissions, secrets, logins, tools, layers, instructions, skills, agents, commands: added, removed, changed) and file-level entries (added, removed, changed, with a unified text diff for small text files). Each entry has a **risk note** where it matters: a new hook command, a new MCP server, a widened permission, a new secret, a new `network.allow` host. The registry runs it on two extracted tarballs; the CLI runs it on a locked version and the new one. The output never contains a secret value: rigs cannot hold one, and text diffs pass through the existing scanner's redactor anyway.
2. **The registry never trusts what it renders.** Diff text is shown escaped in a `<pre>`; the page keeps the strict CSP.
3. **Forks are a relationship derived from the manifest**, not a separate write path: the registry records the `from:` references of each published version and answers "rigs that build on this". "Fork" in the CLI only writes a starter manifest; nothing on the server can create a rig except the existing upload.
4. **Orgs reuse the one visibility predicate.** Membership is one more input to `Viewer`; there is still no read path outside `Visible`.
5. **The UI is loopback-only, token-guarded, and read-mostly.** Same guard as the broker API (Host check, no Origin, bearer token), no inline script, and every action goes through the same `session` code as the CLI.

## Milestones and status

| # | Status |
|---|---|
| S8-M0 | this document |
| S8-M1 to S8-M7 | todo |
