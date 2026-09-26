# Stage 4 plan of record: Git-based sharing, no website yet

**Goal (RIGFILE_PLAN.md §12):** validate demand cheaply. Anyone can `rigfile pull github.com/user/repo[@tag]` a rig onto a new machine, and `rigfile publish --to-git` writes a clean, scrubbed repo from their own setup. The CLI ships on all three OSes.
**Exit:** 5+ external users (at least one each on macOS, Linux, Windows) pull a rig on a new machine; zero secrets leaked in any published test repo (verified by scanning).

Drafted 2026-09-26 on `main` (Stages 1-3 merged). Not started: it starts on the owner's "go".

**What I can and cannot do.** I can build and test everything that runs locally or in CI: fetching, pinning, scrubbing, the checklist, the login flows (against fake servers), release configuration and installer scripts. I cannot: create Apple/Microsoft signing identities, own the Homebrew tap, winget/Scoop/apt repositories or npm/PyPI names, run real OAuth consent screens against vendors, or find 5 external users. Those are owner steps in S4-M6.

## Design calls (decided by my recommendation; each is a row in the log below)

1. **Source syntax:** `github.com/owner/repo[@ref][//subdir]`, also `gitlab.com/...` and any `https://` or `ssh://` git URL. `@ref` is a tag, branch or commit; the **lockfile records the resolved commit SHA and a content hash of the fetched tree**, and `apply` refuses a source whose content no longer matches the lock (a moved tag is an error, not an update).
2. **Fetching:** GitHub/GitLab sources download the commit's tarball over HTTPS (no `git` needed on a fresh machine; the tree hash is verified). Other hosts use the `git` binary. Never `git submodule`/LFS; symlinks in a fetched rig are rejected (already the rule in `hashing`). A per-machine cache under the state dir is content-addressed.
3. **Trust posture for a stranger's rig:** `pull` = fetch + `plan`; nothing runs until the reviewed plan is approved (same screen as today). The plan screen shows the source, resolved commit, and a "from a public repo you did not write" banner; unpinned refs (`@main`) are allowed with a warning and are pinned in the lock. `from:` layers may themselves be remote sources; `rigfile/base-secure` is still prepended and locked, and a remote layer cannot weaken it (existing merge rules).
4. **Publish = capture + scrub + review + write:** built on `capture` (Stage 3), never on a copy of your config directories. Output is a new directory (optionally `git init`ed with a README), never an in-place edit.
5. **Scrub policy:** unresolved secret finding blocks publish, no override for a rig marked public. Personal information (home paths, emails, phone numbers, the OS user name, hostnames) is flagged for review; home paths are rewritten to `~/`.
6. **Installers verify before they run anything:** release archives carry a SHA-256 manifest signed with a **minisign** key (public key embedded in the install scripts and in the binary for `rigfile self-update`); Sigstore/cosign keyless arrives in Stage 6 as planned.
7. **Logins are guided, never automated:** `logins:` entries produce a batched checklist: API key prompt (stored in the secret store), vendor CLI login (run the vendor's own command, then check), OAuth localhost-callback (PKCE, loopback listener, one-shot), device-code flow for headless machines. Tokens go to the secret store; never to a config file.

## Milestones

| # | Milestone | Delivers | Acceptance | Status |
|---|---|---|---|---|
| S4-M0 | **Threat model + spec** | `docs/sharing.md`: source syntax, lock fields, trust rules, scrub rules, what a malicious rig can and cannot do given the plan screen | Reviewed by the owner; every rule has a test named in it | todo |
| S4-M1 | **Sources + fetch** | `internal/source` (parse, resolve tag/branch to SHA, tarball and git fetchers behind an interface, content-addressed cache, tree hash); `layers.Source` for remote `from:`; lockfile fields (`source`, `commit`, `treeSha256`) | Tests with a local fake HTTPS server and a local bare repo; tampered tarball, moved tag, symlink, path traversal, oversized archive, redirect to another host all rejected | todo |
| S4-M2 | **`rigfile pull`** | `pull <source>` = fetch → plan (with source banner) → apply on approval; `--plan-only`; `update` re-resolves within constraints and shows the diff | Container E2E pulls a rig from a local git server; plan writes nothing; lock mismatch refused | todo |
| S4-M3 | **Publish scrubbing** | `internal/publish`: scrub pipeline over a captured rig (secret findings block, personal-info review list, path rewriting), `publish --to-git <dir>` writes a clean repo with README and `.rigfile-allow`-free scan proof | Corpus tests: every secret in `testdata/secrets-corpus` placed in a rig is blocked or replaced; a published repo scans clean with `rigfile doctor --git` | todo |
| S4-M4 | **Publish checklist (TUI)** | grouped, per-item checklist (source path shown, personal-info warnings, "private only" items unticked), final diff, confirm | Scripted-terminal tests (x/term) for keyboard flow; `--yes` refused for public output with warnings | todo |
| S4-M5 | **Guided logins** | `logins:` batch runner; API-key prompt, vendor-CLI login check, OAuth localhost PKCE, device-code for headless; results recorded as needs, never as secrets in state | Tests against fake OAuth/device servers; headless path chosen when no browser/`SSH_CONNECTION`; no token in any log or file | todo |
| S4-M6 | **Release engineering** | GoReleaser config (macOS/Linux/Windows, amd64/arm64), SHA-256 manifest + minisign signing step, install scripts (`install.sh`, `install.ps1`) that verify before running, Homebrew formula, Scoop + winget manifests, `.deb`/`.rpm` via nfpm, npm and pip thin wrappers that download and verify the binary, `rigfile self-update`; release workflow (draft, manual approve) | Dry-run release in CI builds every artifact and the installers verify them in containers/Windows runner; a tampered artifact is refused | todo |
| S4-M7 | **Owner gate** | `docs/stage-4-owner-checks.md`: Apple Developer ID and notarization, a Windows Authenticode certificate, the Homebrew tap, Scoop bucket and winget repositories, npm and PyPI names, generating and safeguarding the minisign key, 5-10 friendly users on three OSes, a feedback log | Exit criteria above | todo (owner) |

## Open items for the owner (not blocking S4-M0..M5)

- Code-signing spend (plan §17 Q10): Apple Developer ($99/year) and a Windows certificate (cost varies; Azure Trusted Signing is an option). Until then release binaries are unsigned and installers say so.
- Which GitHub org/repo hosts releases, the Homebrew tap and the Scoop bucket.
- LICENSE (still open from Stage 2) must be decided before the first public release.
