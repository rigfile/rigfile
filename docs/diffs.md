# Version diffs (S8-M1)

`internal/rigdiff` answers one question: *what does this version of a rig do that the one before did not?* It is used by `rigfile changes`, by `rigfile update` (the review banner), and by the registry (`GET /v1/rigs/{owner}/{name}/diff` and the page `/r/{owner}/{name}/diff`).

## What is compared

- **Manifest items**, by identity, not by position: instructions, skills, agents, commands, MCP servers, hooks, permission rules (deny, ask, allow separately), tools (per package manager), secrets, logins, models, gateways, base rigs (`from`), targets, overrides, routing, and the version, description and license. Each is `added`, `removed` or `changed`.
- **Files**, by content hash: added, removed, changed. `rigfile.yaml` is covered by the manifest comparison and `rigfile.lock` is machine written, so neither is listed; `.git` is skipped. A changed or added text file up to 256 KiB and 3000 lines gets a unified diff (three lines of context); a binary or larger file is reported by size only; the total diff text is capped at 512 KiB.

## Notes that ask for a look ("review")

The rules are few and plain. They say what the update can **run**, **reach**, **allow**, **ask for**, or make the agent **follow**:

| Change | Note |
|---|---|
| MCP server added | it runs this command |
| MCP server's command, args, URL or transport changed | what it runs changed, before and after |
| a host added to `network.allow` | may now reach that host |
| `network.allow` removed | no longer declares an allowlist |
| a secret newly read into an env var | reads secret X into Y |
| hook added or changed | runs a command on an event |
| `allow` rule added | allows this without asking |
| `deny` rule removed, `ask` rule removed | removes a protection, no longer asks |
| secret's bound hosts changed | changes where the secret may be sent |
| package added to `tools` | installs this package |
| base rig added or removed | changes what the rig is built on |
| instruction, skill, agent or command added or changed, and the files they point to | text the agent will follow |
| script file (`.sh`, `.ps1`, `.py`, `.js` ...) added or changed | a script |

Removals are information, not review items. Version, description and licence changes carry no note. Everything else that differs is still listed, without a note.

## Safety

- Nothing in a diff is a secret value: rigs cannot hold one (schema and scan), and diffs are shown only for versions the scanner accepted.
- The registry decides who may see a diff with the same visibility predicate as every other read: both versions must be visible to the viewer (a pending version is visible only to its owner), and a private or missing rig is an identical 404. The result cache is keyed by the pair of immutable tarball hashes and consulted only after both visibility checks.
- A diff extracts two archives, so it is rate limited per client (30 a minute, burst 10), at most three run at once, and extraction uses the hardened extractor (no traversal, links, devices; entry and size limits).
- The page shows text escaped, in `<pre>` blocks, under the registry's strict Content-Security-Policy.

## Tests

| Rule | Test |
|---|---|
| Each kind of change is found and flagged as described | `TestManifestChangesAndTheirNotes` (`internal/rigdiff`) |
| Nothing changed means nothing shown; removals are not alarming; `.git` and the lock are ignored | `TestIdenticalRigsHaveNoChanges`, `TestRemovalsAreNotAlarming`, `TestDotGitAndTheLockAreIgnored` |
| Hunks, context, new files, size limits | `TestUnifiedDiffHunksAndLimits`, `TestBinaryAndLargeFilesAreNotDiffed` |
| `rigfile update` shows what changed, `--diff` shows text | `TestUpdateFollowsTheSourceAndSaysWhenNothingChanged` |
| `rigfile changes` on directories | `TestChangesComparesTwoRigDirectories` |
| The registry: defaults, both directions, escaping, rate limit, pending and private versions | `TestDiffBetweenVersions` |
