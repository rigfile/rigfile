# Stage 4 owner checks (S4-M7)

Everything the build could do without your accounts, signing identities or other people is done and tested (see `docs/stage-4-plan.md`). What follows needs you. Where a step says **settles**, it turns an UNVERIFIED into a fact; record the date in the named doc.

## 1. Push and read CI

`git push -u origin stage-4`. New in CI: `installer (real minisign)` (Docker on `ubuntu-latest`), and `tools/release` tests on all three OSes (they build real binaries, so expect ~30 s more per OS). The first Windows run of the new packages (`internal/source`, `internal/login`, `internal/selfupdate`, `internal/tui`, `tools/release`) is the first native Windows evidence for them; send me any red log.

## 2. Decide (blocks the first public release)

| Decision | Why it blocks | Where it lands |
|---|---|---|
| ~~**LICENSE**~~ decided 2026-09-26: Apache-2.0 | was: every package manifest carried `TODO-owner` for the licence | `LICENSE`, `tools/release/packaging.go` |
| ~~**Package names**~~ decided 2026-09-26: GitHub org `rigfile`; winget `Rigfile.Rigfile`; tap `rigfile/homebrew-tap` (`brew install rigfile/tap/rigfile`); npm and PyPI `rigfile`. You still reserve the npm and PyPI names (and `rigfile-cli` as a defensive placeholder) with your own logins | was: | names may be taken; winget needs a publisher id | `tools/release/packaging.go` (`TODO-owner` markers) |
| **Maintainer contact** for the `.deb` | `Maintainer:` is a placeholder | `tools/release/deb.go` |

## 3. Release signing key (minisign)

1. Install minisign, then `minisign -G -p rigfile.pub -s rigfile.key` (use a password).
2. GitHub repository settings: secret `MINISIGN_SECRET_KEY` (the key file's contents), secret `MINISIGN_PASSWORD`, variable `MINISIGN_PUBLIC_KEY` (the **last line** of `rigfile.pub`, one base64 line). Create an environment named `release` with you as a required reviewer.
3. Keep an offline backup of `rigfile.key` and its password. **If it is lost or leaked**: every installed binary trusts this key (it is compiled in), so recovery means shipping a new binary through a channel users trust (package managers) that carries a new key. Write the incident plan before the first public release.
4. Run the `release` workflow (Actions tab, version `0.0.1-test`) as a **dry run on a fork or a scratch repository first**: it builds, signs and creates a *draft* release; check the assets, then delete the draft.

**Settles:** the release workflow end to end, including `gh release create` with your token.

## 4. Code signing (macOS and Windows) — cost and steps not done by me

- **macOS**: Apple Developer Program ($99/year) gives a Developer ID certificate. Notarization applies to `.zip`/`.pkg`/`.dmg`, not to a bare binary inside a `.tar.gz`. Files fetched with `curl` (the installer, Homebrew) carry no quarantine flag, so they run without notarization; a binary downloaded in a browser will show the Gatekeeper warning until you notarize and staple. Recommended: codesign + notarize the darwin binaries before archiving (add `codesign`/`notarytool` steps to `release.yml` on a macOS runner; I did not, because I cannot test them without your certificate).
- **Windows**: an Authenticode certificate (OV/EV) or Azure Trusted Signing. Without it SmartScreen warns on `rigfile.exe` from a browser; `winget` and Scoop installs are less affected. Add a signing step for `rigfile.exe` before zipping.

Until then releases are honest about it: the installers verify the minisign signature and SHA-256, which does not depend on either certificate.

## 5. Publish to the package channels (after 2 and 3)

The release writes ready-to-submit manifests to `dist/packaging/` (uploaded by the workflow as the `packaging-manifests` artifact), each with real hashes.

| Channel | You do | Check first |
|---|---|---|
| Homebrew | create `rigfile/homebrew-tap`, copy `homebrew/rigfile.rb` into `Formula/` | `brew audit --strict --new rigfile` and `brew install --build-from-source`, `brew test rigfile` |
| Scoop | create a bucket repository, copy `scoop/rigfile.json` into `bucket/` | `scoop install ./rigfile.json` on Windows |
| winget | pull request to `microsoft/winget-pkgs` with `winget/manifests/...` | `winget validate --manifest <dir>`; sandbox test |
| npm | `cd dist/packaging/npm && npm pack && npm publish` (2FA on) | install the tarball on a clean machine: the postinstall downloads from the GitHub release and refuses a checksum mismatch |
| PyPI | `twine upload dist/packaging/pip/*.whl` | `pip install` each wheel on its platform, then `rigfile version` |
| apt | host the `.deb` (a signed apt repository is not built: choose Cloudsmith/packagecloud/a static repo) | `dpkg -i` on Ubuntu, `dpkg -c` |
| dnf/rpm | **not built.** Add an `nfpm` step (`rpm` format) once you want it | — |

**Settles:** the winget manifest schema version (1.6.0 written from memory of the schema, UNVERIFIED), Homebrew `on_macos`/`on_linux` blocks, the Scoop `autoupdate` block, npm `postinstall` on all three OSes (only its syntax and the checksum logic are tested here), the wheel platform tags.

## 6. Live checks I could not run (each is an UNVERIFIED)

| # | Check | Command | Settles |
|---|---|---|---|
| 1 | GitHub source: `Accept: application/vnd.github.sha` resolve, tarball redirect to `codeload.github.com`, private repo with `GITHUB_TOKEN` | `rigfile pull github.com/<you>/<a small public rig repo> --plan-only`, then a private one | `docs/sharing.md` §2/§4 host allowlist |
| 2 | GitLab source: default-branch lookup and archive endpoint | `rigfile pull gitlab.com/<group>/<project> --plan-only` | same |
| 3 | An https git URL on another host, and an `ssh://` URL with your key | `rigfile pull ssh://git@github.com/<you>/<repo>.git --plan-only` | git fetcher (`git archive` by commit needs the server to allow it; the fallback fetches the ref) |
| 4 | `rigfile self-update --check` against a real signed release | after step 3.4 | minisign interoperability is already proven with the real tool (installer E2E); this checks the GitHub URLs |
| 5 | `install.sh` on macOS and `install.ps1` on Windows against a real release | see the header of each script | the PowerShell script has never been executed by anyone |
| 6 | Vendor logins: `rigfile logins` for `github` (`gh auth login`), `codex` (`codex login`), `claude-code` (manual `/login`) | apply a rig that lists them, then `rigfile logins` | the vendor commands in `internal/login` (`codex login` is documented; a status probe for Codex is not) |
| 7 | OAuth loopback and device flows against a real provider | no provider is registered in this repository (each needs a public client id issued to Rigfile); the flows are tested only against fake servers | needs the owner to register OAuth apps |

## 7. The exit criterion: 5+ external users

- 5-10 friendly people, at least one each on macOS, Linux and Windows.
- Give each: the install one-liner from the draft release, and one rig to pull (publish one of yours with `rigfile publish --to-git`).
- Ask them to send: OS and shell, the exact commands they ran, what the plan screen showed that surprised them, anything that failed, and whether they understood what they were approving.
- Record each in a table in `docs/STATUS.md`: date, OS, pulled rig, result, notes.
- **Zero secrets leaked** in any published test repo: run `rigfile doctor --git <repo> --max-commits 5000` on each repository you publish while testing, and keep the output.

## 8. Exit criteria (plan §12, Stage 4)

| Criterion | Evidence today | Open |
|---|---|---|
| 5+ external users pull a rig on a new machine (one per OS) | container E2E of publish → pull → update → rollback on Ubuntu and Fedora; unit and CLI tests for every step | real users (§7), Windows/macOS runs |
| Zero secrets leaked in any published test repo | publish blocks any scanner finding (all 100+ core corpus secrets are blocked), and re-scans its own output | your test repositories (§7) |
