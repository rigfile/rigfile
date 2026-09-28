# Sharing rigs through git: threat model and spec (S4-M0)

Status: spec of record for Stage 4, implemented on branch `stage-4`. Each rule is enforced by tests in `internal/source`, `internal/layers`, `internal/publish`, `internal/login`, `internal/selfupdate` and `cmd/rigfile` (test names in the sections below are the primary ones); a rule without a test is a bug in this document.

## 1. What a rig is, from a stranger's point of view

A rig is text plus a manifest. Applying one can put **instructions** in front of an agent, install **hooks and scripts** that run on the user's machine, register **MCP servers** (arbitrary commands), and widen or narrow **permissions**. Pulling a stranger's rig is therefore running a stranger's code with the user's rights, gated by one thing: the plan screen the user approves. Stage 4 does not change that gate; it makes sure the screen describes the right bytes.

## 2. Source syntax

`rigfile pull <source>` where source is one of:

| Form | Example | Fetched by |
|---|---|---|
| GitHub | `github.com/owner/repo`, `github.com/owner/repo@v1.2.0`, `github.com/owner/repo@main//sub/dir` | HTTPS tarball (no `git` needed) |
| GitLab | `gitlab.com/owner/repo@tag` | HTTPS tarball |
| Any git URL | `https://host/path/repo.git@ref`, `ssh://git@host/path/repo.git@ref` | the `git` binary |

`@ref` is a tag, branch or 40-hex commit; empty means the default branch. `//subdir` selects a directory that holds `rigfile.yaml`. `file://` URLs are accepted only for the git fetcher (used by tests and local mirrors).

## 3. Pinning (tests: `TestPinnedByCommitAndTree`, `TestMovedTagIsRefused`)

- The ref is resolved to a **commit SHA** once; the tree at that commit is downloaded and hashed (`treeSha256`, independent of execute bits and line endings so it is identical on macOS, Linux and Windows).
- `apply` from a pulled rig records `source`, `commit` and `treeSha256` in `state.json`; `rigfile update` re-resolves the same source and shows a normal plan of the difference. A tag that now points to a different commit is reported as "the tag moved", never applied silently.
- The cache is content-addressed by `treeSha256`; a cache entry whose files no longer hash to its name is deleted and re-fetched.

## 4. Fetch safety (tests: `TestExtract*`, `TestRedirect*`, `TestGit*`)

| Attack | Rule |
|---|---|
| Path traversal / absolute paths in the archive | rejected (`..`, absolute, drive letters, backslashes, NUL) |
| Symlinks, hard links, devices, FIFOs | rejected, never created |
| Zip/tar bombs | at most 5,000 entries, 5 MiB per file, 50 MiB in total; the download itself is capped at 50 MiB |
| Case-fold collisions (`A.md` vs `a.md`) | rejected (the rig would differ between macOS/Windows and Linux) |
| Windows-reserved names (`con`, `nul`, `aux.txt`) | rejected |
| Redirects to another host | only within the provider's own hosts (`github.com`, `codeload.github.com`, `api.github.com`; `gitlab.com`); anything else fails |
| Non-HTTPS | refused (except `file://` for git) |
| Git tricks | fetch by exact commit into a fresh empty repository, `git archive` (no checkout, so no hooks and no smudge filters), submodules and LFS never followed, `protocol.ext.allow=never`, credential helpers disabled |
| Credentials | `GITHUB_TOKEN`/`GITLAB_TOKEN`, if set, are sent only to the provider's own API host, never to redirect targets |

## 4b. Remote `from:` layers

`from: [github.com/owner/repo@ref]` resolves through the same fetcher and pins into `rigfile.lock` (`source`, `commit`, `treeSha256` per layer). A remote layer is untrusted like any other: `rigfile/base-secure` is still the lowest, locked layer and no remote layer can remove or weaken it (test: `TestRemoteLayerCannotWeakenBase`). The lock refuses a layer whose fetched tree no longer matches.

## 5. Trust posture on the plan screen (tests in `cmd/rigfile/pull_test.go`)

- The screen opens with `Source: github.com/owner/repo @ <commit12> (tree <sha12>)` and, for a source that is not the user's own login, a banner: **"This rig comes from a public repository you did not write. Review every hook, script and MCP command below."**
- `pull` never applies without the normal approval (`--yes` is allowed, as for local rigs, but the banner still prints).
- Unpinned refs (branch or none) print a warning and are pinned by the commit in the lock.
- Nothing from the rig executes during `pull` before approval: no script is run to "prepare" it, no `postinstall`.

## 6. Publish (`rigfile publish [<dir>]`; GitHub is the default destination) (tests: `internal/publish`)

Pipeline, in this order; any failing stage stops before anything is written:

1. **Source**: a rig directory, or (default) a capture of this machine's tools (`capture`, the reverse of the adapters). State that Rigfile itself applied is left out, so a rig does not swallow what an earlier rig installed. Refused if `state.json` says base-secure was skipped (`--i-understand-unsafe-base`).
2. **Select** (TUI checklist, or `--all`): items are grouped by category with their origin; personal-looking ones (`CLAUDE.md` with personal content) start unticked.
3. **Scrub**:
   - **secrets**: every text file and every manifest value goes through the scanner (all rules, decoding depth 2). A finding **blocks** publishing; there is no override for public output. Credential-like env values and headers were already turned into `secret://` references by capture.
   - **personal information**: absolute home paths become `~/`; e-mail addresses, phone numbers, the OS user name and the host name are listed for review and block until each is acknowledged (`--ack-personal`, or ticked in the TUI).
   - **rig hygiene**: `manifest.Check` must pass with zero errors; MCP packages must be version-pinned; no absolute machine paths remain.
4. **Write** into a scratch directory: `rigfile.yaml`, the referenced files only, a generated `README.md` (what is inside, how to pull it, what secrets/logins it needs), and a `.gitignore`. Nothing is copied wholesale from a config directory.
5. **Prove**: the output tree is scanned again from disk; publishing fails if that scan finds anything (`scan proof: 0 findings, N files` is printed).
6. **GitHub, the default destination** (suppressed only by an explicit `--to-registry` without `--to-github`, `--write-tarball` alone, or `--dry-run`): `gh auth status` first (the same command `rigfile logins` checks with); on failure, `gh auth login` (interactive) before continuing — a signed-in caller sees neither. `--to-github <owner>` names the owner explicitly; otherwise it comes from `gh api user -q .login`. Then `git init` and one commit through the user's own hooks, and `gh repo create <owner>/<repo-name> --private|--public --source=<dir> --remote=origin --push` (the repo name is the published rig's own `name:`, not a separate argument). The scratch directory is removed afterward. `--write-tarball <file>` writes the same scrubbed archive without publishing anywhere, for a manual push to a different host or for signing before `--to-registry`.

## 7. What Stage 4 does not defend against

- A malicious rig whose harm is visible on the plan screen but which the user approves anyway. Static analysis of hooks and scripts, signatures on rigs, verified publishers and reputation are Stage 6.
- A compromised GitHub account or repository at the moment of first pull (the pin protects later pulls, not the first).
- The user pasting a secret into a public repo by hand after `publish`. The dogfood git hooks from Stage 2 are the defence there.

## 8. Logins (S4-M5)

`logins:` entries are walked by `rigfile logins` after `apply`/`pull` (the list comes from `state.json`). The manifest methods are `api-key` (hidden prompt, stored in the secret store as `logins/<provider>/api_key`), `vendor-cli` (Rigfile runs the vendor's own command and then its status check: `gh auth login`+`gh auth status`, `codex login`; `claude-code` and `gemini-cli` are manual steps Rigfile explains and asks about) and `oauth` (loopback listener on `127.0.0.1` with PKCE and a one-shot `state`; the RFC 8628 device flow is chosen instead when there is no browser or `SSH_CONNECTION` is set). No OAuth provider is built in: each needs a public client id issued to Rigfile, so `oauth` answers "no OAuth client is registered" until the owner adds one. Tokens go to the secret store only; nothing is written to a config file, log or `state.json` (which keeps names of needs, never values). Tests run against fake OAuth servers.

## 9. Release verification (S4-M6)

Release archives are listed in `SHA256SUMS`, signed with a minisign key whose public half is embedded in the binary (`selfupdate.PublicKey`, set at build time) and written into the release's `install.sh`/`install.ps1`. Installers and `rigfile self-update` verify the signature, check that its signed trusted comment names the version being installed (an old signed list cannot be replayed under a newer tag), then the archive hash, and refuse otherwise (tests: `internal/selfupdate` and the container installer E2E with the real minisign: tampered archive, edited checksums, wrong key, replayed signature, missing signature, key-less template). The installers need the `minisign` tool; without it they refuse unless `RIGFILE_INSECURE_SKIP_SIGNATURE=1` is set, which skips only the signature check and says so. A build without a key refuses to self-update.
