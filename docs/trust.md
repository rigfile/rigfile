# Trust and supply chain: spec (S6-M0)

Status: spec of record for Stage 6, implemented on branch `stage-6`; §10 lists where the build differs from the first draft. Threat background: RIGFILE_PLAN.md §11 and `docs/registry.md` §7. This document says what the registry and the CLI will tell a person deciding whether to run a stranger's rig, and how each statement is established.

## 1. What can go wrong with a rig, and which layer answers it

| Risk | Layer that answers | Where the answer is shown |
|---|---|---|
| Secrets in the rig | scan (Stage 5): rejects | publisher sees findings; nobody else ever sees the version |
| Malicious hook, script or MCP command | static analysis (§2), package lookup (§6), the human reading the plan | rig page, pull screen |
| Hostile instruction text (prompt injection, hidden text) | static analysis of instructions (§2) | rig page, pull screen |
| Look-alike name | similar-name detection (§4) | upload response, rig page, pull screen |
| Publisher is not who they seem | signature identity (§3), verified badge (§5) | rig page, pull screen, lock |
| Account takeover / rug pull | immutable versions, hash pins (Stage 4/5), signatures tied to a GitHub identity, required for popular rigs | pull screen, lock |
| Known-bad dependency | OSV lookup (§6) | rig page, pull screen |
| The registry itself is compromised | the CLI verifies the tarball hash it pinned, and re-verifies signatures locally against Sigstore, not against the registry's word | CLI |
| An incident is under way | pause, revoke, remove tools and the runbook (§8) | operator |

## 2. Static analysis (`internal/analyze`)

Input: a rig directory (or the files of a tarball). Output: a list of findings `{level, rule, file, line, message}`; **no finding contains any text from the analysed file** (messages are fixed strings per rule, plus counts), so the report cannot itself carry an injection or a secret.

Levels: **notice** (worth knowing), **caution** (likely to matter; read before approving), **danger** (almost never legitimate in a shared rig).

Rule groups and examples (each rule id is stable; each has a positive and a negative fixture):

| Group | Examples | Level |
|---|---|---|
| Network | `curl`/`wget`/`Invoke-WebRequest`/`nc`/`/dev/tcp`, Python `requests`/`urllib`/`socket`, Node `fetch`/`http`, DNS lookups | notice; caution when the destination is a variable, a raw IP or a paste/URL-shortener host |
| Download and run | `curl … \| sh`, `iex (iwr …)`, `bash <(curl …)`, `python -c "$(curl …)"` | danger |
| Obfuscation | `base64 -d \| sh`, `eval`, `exec(base64…)`, long base64/hex blobs, `chr()`/`fromCharCode` assembly, string reversal into exec | caution; danger when decoded content is executed |
| Credential and dotfile access | reads of `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.netrc`, keychains, browser profiles, `.env` outside the project, `printenv`/`os.environ` dumps | caution; danger when combined with a network rule in the same file |
| Persistence and tampering | `crontab`, `launchd`, `systemd`, Windows Run keys, edits to shell rc files, `~/.claude`, `~/.gitconfig`, git hooks, `core.hooksPath`, `--no-verify` | caution; danger for hooks paths and rc files |
| Privilege | `sudo`, `chmod 777`, `Start-Process -Verb RunAs` | caution |
| Filesystem reach | writes or deletes outside the project directory, `rm -rf` on `~` or `/` | caution / danger |
| Rig structure | hooks that run on every tool call with network access; MCP servers using `http://` (non-TLS, non-loopback), unpinned launchers, `bash -c` commands; `permissions.allow` of `Bash(*)` or removal of protections | notice / caution |
| Instruction text | "ignore previous instructions", "do not tell the user", "hide this from the user", requests to read or send credentials or files, requests to disable safety or hooks; **hidden characters** (zero-width, bidirectional overrides, tag characters), HTML comments carrying instructions | caution; danger for bidi overrides and tag characters |

Honesty: static analysis is a heuristic. A determined attacker can evade every rule; a benign rig can trip some. The rules exist to make the *obvious* things visible and to raise the cost of the rest. The measured precision and recall on the fixture corpus are recorded in `docs/analysis-metrics.md`, the same way `docs/scanner-metrics.md` does for secrets.

## 3. Signatures

**Format:** Sigstore bundle (protobuf JSON, media type `application/vnd.dev.sigstore.bundle.v0.3+json`), produced by `cosign sign-blob --bundle`, signing the **tarball bytes** (message signature over the artifact digest). The registry does not re-sign or transform it.

**Verification (`internal/sigverify`, sigstore-go):** the bundle must verify against the Sigstore trusted root (Fulcio CA chain, signed certificate timestamp, Rekor inclusion proof / integrated timestamp) for the artifact, and the certificate's identity must satisfy the **policy**:

- issuer: exactly `https://token.actions.githubusercontent.com` (GitHub Actions) or `https://github.com/login/oauth` (GitHub sign-in through Sigstore's OIDC) (others are recorded but never treated as "the publisher");
- subject: for GitHub Actions, `https://github.com/<publisher-login>/<repo>/.github/workflows/<file>@<ref>`; for a GitHub OIDC login, the subject must be an identity the registry has tied to the publisher's account (the account's verified primary email as reported by GitHub at sign-in). A subject that is valid but belongs to someone else is **signed by someone other than the publisher**.

**Where verified:** at upload by the registry (to show the status and to enforce the policy for popular rigs), and **again on every pull by the CLI**, using its own copy of the trusted root (fetched through Sigstore's TUF root of trust, cached on disk; a pinned snapshot for offline use). The CLI prints `signed by <subject> (<issuer>)` on the plan screen and writes `signer` (issuer and subject), `bundleSha256` and the Rekor log index into `rigfile.lock`; a later resolution of the same version with a different signer is refused like any other lock mismatch.

**Policy switches:** `rigfile pull --require-signature` and a `require_signature` setting refuse unsigned or wrongly signed rigs. The registry's popular-rig rule (§5) requires a valid publisher signature for new versions of a rig above a star threshold.

**Not done:** Rigfile does not run the OIDC flow or call Fulcio/Rekor to *create* signatures; cosign does. Signing the CLI's own releases with Sigstore (in addition to minisign) is a release-workflow change, not new code.

## 4. Similar names

For a new rig `owner/name` (and for every pull), compare against existing **public** rigs and owners:

- normalise: lowercase, remove `-`, `_`, `.`; map confusable characters (`0/o`, `1/l/i`, `rn/m`, Cyrillic and other homoglyphs where a name could contain them);
- flag when the normalised name equals another's, or the Damerau-Levenshtein distance is ≤ 1 (≤ 2 for names of 8+ characters), or one is the other plus a common suffix/prefix (`-official`, `-pro`, `-v2`, `-secure`);
- weight by the other rig's stars: a match is only "notable" if the other rig has stars or a verified publisher.

Result: `similar_to: [owner/name]` on the version, shown to the publisher in the upload response, on the rig page, and on the CLI pull screen ("similar to jiaxu/data-science, which is verified and has 340 stars"). A **new public rig confusably equal (after normalisation) to a verified or popular rig** is `held`.

## 5. Verified publishers, popularity, policy

- **Verified publisher:** set by an administrator (`admin verify-publisher --login L --note "..."`), shown as a badge with the note's kind (domain / organisation / known person; the note text is not published). Revocable. Logged.
- **Popular:** a rig at or above `POPULAR_STARS` (configurable). New versions of a popular rig need a valid publisher signature (§3) *and* a verified publisher; otherwise the version is `rejected` with a message explaining why. Below the threshold both are optional.
- Stars can be gamed; the threshold is a soft trigger, not a reputation system. The policy exists so that the *takeover of an account that already has an audience* costs the attacker a signing identity, not just a password.

## 6. Package lookups (OSV)

Pinned MCP launcher packages (`npx pkg@1.2.3`, `uvx pkg==1.2.3`, `pipx run pkg==1.2.3`) are extracted, and each `(ecosystem, name, version)` is queried against OSV (`api.osv.dev`): a `MAL-` advisory (known malicious package) rejects the version; other advisories are warnings with id and severity; an unreachable or malformed answer is recorded as "package check unavailable". Outbound requests go only to the configured OSV host with a short timeout; response size is capped; package names are validated against ecosystem patterns before use.

## 6b. Reputation facts on the pull screen

Age of the rig and of the version; number of versions; stars; publisher's first-seen date and number of public rigs; verified badge; signature status; analysis summary counts by level; whether any version of this rig or any rig of this publisher was ever removed by moderators (counts, not reasons). No score.

## 7. Moderation queue

Status `held` joins `pending`, `published`, `rejected`, `yanked`, `removed`. It is treated exactly like `pending` by the visibility predicate (owner and admins only). Causes: danger-level analysis results, or the confusable-name rule (§4). `/admin` (administrators only, session + CSRF) lists held versions and open reports with **release** (→ `published`), **reject** and **remove** actions; the same operations exist in `rigfile-registry admin`. Every action is audited.

## 8. Incident response tooling

`admin publishing pause|resume` (uploads refused with 503 and a message), `admin revoke-tokens --login L | --all`, `admin disable-user`, `admin takedown`, `admin audit`; documented procedures for a compromised publisher account, a malicious rig in circulation, a leaked registry credential, a compromised release-signing key and a compromised registry host, in `docs/incident-response.md`.

## 9. Tests (named)

| Rule | Test |
|---|---|
| Findings never contain analysed text | `TestFindingsNeverEchoTheFile` |
| Each analysis rule fires on its positive fixture and not on its negative | `TestRulesFirePositivesAndNotNegatives` |
| Corpus precision/recall are as documented | `TestAnalysisCorpusMetrics` |
| Hidden characters are found | `TestHiddenCharacters` |
| Similar names: confusables, delimiters, distance, popularity weighting; unrelated names are quiet | `TestSimilarNames` |
| Valid bundle verifies; wrong artifact, wrong identity, untrusted root, tampered bundle, non-publisher signer fail | `TestVerifyBundle*` |
| The CLI re-verifies and records the signer; a changed signer is refused | `TestPullRecordsAndChecksTheSigner` |
| Popular rigs need signature and verified publisher | `TestPopularRigPolicy` |
| `held` is invisible except to owner and admins; release and reject work; admin page needs CSRF | `TestHeldVersions`, `TestAdminPage` |
| OSV: malicious rejects, vulnerable warns, unavailable is reported, hostile answers ignored | `TestPackageLookups` |
| Pause blocks uploads; mass revocation works | `TestPublishingPause`, `TestRevokeTokens` |

## 10. As built: differences from the draft above

- **Publisher identity is GitHub Actions only.** A signature counts as "the publisher's own" when the certificate's issuer is `https://token.actions.githubusercontent.com` and its subject is a workflow in a repository owned by the publisher's GitHub login (`sigverify.PublisherIdentity`). The draft also allowed a GitHub OIDC login mapped through the account's e-mail; the registry asks GitHub for no scope, so it cannot know the e-mail, and such signatures verify but are shown as "signed, but not by the publisher's identity".
- **Signing flow:** `rigfile publish <rig> --write-tarball rig.tgz` writes the exact bytes that will be uploaded (deterministic), `cosign sign-blob --bundle bundle.json rig.tgz` signs them, `rigfile publish <rig> --to-registry --sign-bundle bundle.json` uploads both (multipart). The registry verifies before accepting; the CLI verifies again on every pull.
- **Registry pins are not re-verified from cache.** A pinned re-fetch that is served from the local cache (registry layers referenced from a lock) does not repeat the signature check; the top-level `pull` always downloads and verifies.
- **Signer recorded in `state.json`**, not in `rigfile.lock` (the lock records layers' source, commit and tree hash). `rigfile update` refuses a changed signer unless `--accept-signer-change`.
- **The `held` queue applies to public rigs only** (danger-level analysis on a new version). A private rig with a danger finding is published to its owner with the analysis attached; **going public** with danger findings, or with a name that is a look-alike or affix of a notable rig, needs an administrator (`ApprovePublic`), which files a review request automatically.
- **Popular rigs** (`RIGFILE_REGISTRY_POPULAR_STARS`, default off): new versions of a public rig at or above the threshold are rejected unless signed by the publisher's Actions identity **and** the publisher is verified.
- **OSV**: exact pinned packages from `npx`/`bunx`/`pnpm dlx`/`uvx`/`pipx run`/`uv run` are looked up; an advisory id starting `MAL-` rejects (UNVERIFIED against the live API), other advisories warn, an unreachable OSV is a visible warning. Configured with `RIGFILE_REGISTRY_OSV_URL` and `RIGFILE_REGISTRY_OSV=off`.
- **Not built:** `admin rescan` of stored versions, reputation signals that need download counts, publisher-account age from GitHub (the registry's own first-seen date is shown), the OIDC-signing client, signing Rigfile's own releases with Sigstore.
