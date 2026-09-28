# Registry security self-review (S5-M6)

**This is my review of my own work. It is not the external security review the Stage 5 exit criterion requires** (`docs/owner-checklist.md`). Its job is to hand a reviewer a map: what protects what, where the evidence is, and what is known to be weak. Date: 2026-09-26, branch `stage-5`.

Method: I walked `docs/registry.md` §7 row by row, wrote a test for every claim I could test, ran `go vet`, the race detector, `govulncheck ./...` (no reachable vulnerabilities; `golang.org/x/crypto` upgraded to v0.56.0 for the two fixable advisories in a module we require but do not call; GO-2026-5932 has no fix yet and is not called), and the container end-to-end run (`e2e/registry.sh`).

## 1. Authentication

| Claim | Evidence | Confidence |
|---|---|---|
| OAuth `state` is random, bound to a cookie, checked in constant time, single use | `TestOAuthStateIsChecked` (wrong state, replay, no cookie, GitHub rejects the code) | high |
| Post-login redirect cannot leave the site | `TestOpenRedirectsAreRefused` (`//host`, `https:`, `\host`, `javascript:`, `http:host`) | high |
| Session cookies: `HttpOnly`, `SameSite=Lax`, `Secure` + `__Host-` prefix over HTTPS, hashed at rest, 7-day expiry, revoked on logout and when the account is disabled | `TestSessionsExpireAndAreStoredHashed`, `TestSecurityHeaders`, `TestCSRF` | high |
| API tokens: 256-bit random, only SHA-256 stored, 30-day expiry, revocable, stop working when the account is disabled | `TestTokensAreHashedExpireAndRevoke`, `TestAdminTools`, E2E logout revokes server-side | high |
| Device flow (RFC 8628): single-use codes, `slow_down`, expiry, denial; user code alphabet without look-alikes; entry rate limited | `TestDeviceFlow`, `TestDeviceFlowOverHTTP` | high for logic; the user-code space is ~34 bits, so its safety rests on the 15-minute life and the rate limit (below) |
| The registry asks GitHub for **no OAuth scope** (public profile only) | `TestOAuthStateIsChecked` asserts no `scope` parameter | high |
| CSRF: every state-changing web form needs the session's token and a same-origin `Origin`/`Referer`; the API uses bearer tokens, which browsers do not attach on their own | `TestCSRF`, `TestStarAndVisibilityFormsNeedTheSessionCSRFAndOrigin` | high |

Known weak points:
- **GitHub login reuse. Closed 2026-09-26 (owner decision: reserve vacated logins).** The account follows the numeric GitHub id, and rigs are keyed by the login string. When someone renames their GitHub account, the old login stays reserved for that account (migration 0005, table `login_reservations`, enforced by a trigger under the same advisory lock as the user/organisation namespace), so a stranger who takes the freed GitHub name is refused at sign-in ("Name reserved") and cannot rename into it or create an organisation with it. Only a login that owns at least one personal rig is reserved: that is what other people trust, and it stops anyone squatting names by cycling through GitHub logins. Renames that happened before the migration are backfilled from `rigs.owner`. The original account can rename back; an operator frees a login with `rigfile-registry admin release-login --login NAME` (audited), for a departed account or a settled dispute. Existing rigs were already safe: ownership is checked by `created_by`. Residual: the registry learns of a rename only at the account's next sign-in, so a stranger who takes the name *before* that sign-in wins the race; a rig-owning account that renames should sign in to the registry promptly. Keying namespaces by id would remove this window and remains the stronger design if the registry grows.
- **No second factor** beyond GitHub's. Stage 6 plans 2FA policy for popular rigs.
- The operator's `admin token` command mints a token for any account. That is a deliberate break-glass tool: it needs shell access to the deployment and database credentials, and it writes an audit entry (`admin.token`).

## 2. Upload and scanning

| Claim | Evidence |
|---|---|
| Hostile archives (traversal, absolute paths, symlinks, devices, bombs, oversized, non-gzip) are refused before anything is stored | `TestUploadRefusesHostileArchives`; the extractor is the Stage 4 one (`internal/source`, its own attack tests) |
| The manifest must be valid, error-free and match owner/name; `rigfile/*` layers other than base-secure are refused | `TestUploadNamespaceRules`, `TestUploadRefusesHostileArchives` |
| Reserved owners need an admin | `TestReservedNames` |
| Versions are immutable, including under concurrent uploads (a database unique constraint, not a check-then-insert) | `TestVersionsAreImmutable`, `TestMigrationsApplyOnceAndConstraintsHold` |
| A secret anywhere rejects the version; findings and the database never contain the value | `TestScanRejectsSecretsWithoutLeakingThem`; every core corpus secret blocks (`internal/publish` corpus test) |
| Public rigs must pin packages; private ones only warn; going public re-checks the newest version | `TestPinningPublicVersusPrivate` |
| A crashed worker's job is retried; two workers never take the same job; repeated failure rejects rather than looping | `TestWorkerRecoversAndLocks` |
| A rejected upload's archive is deleted at once (owner decision 2026-09-26); the reason (never the secret value) stays on the version row; a blob still referenced by another version is kept | `TestRejectedUploadArchiveIsDeletedAtOnce`, `TestRejectedUploadKeepsABlobStillUsedByAnotherVersion`, `internal/registry/blob` `contract` (Delete) |

Known weak points:
- The scan covers secrets, manifest validity and pinning. **It does not analyse hooks or scripts for malicious behaviour, and does not check package reputation or typosquatting** (Stage 6). A rig can pass the scan and still be harmful; the pull screen and the plan screen are the defence today, and the site says so.
- Upload bodies are read into memory (≤20 MiB by default) before validation. With the per-user rate limit this is bounded, but a distributed flood of authenticated uploads could use memory; set the container memory limit accordingly.
- The scanner has a per-file size cap and a 2-minute time box per job; a pathological file that defeats the scanner's regexes could take the full time box. The corpus timing gate (Stage 2) is the evidence for typical input.

## 3. Reads, visibility, output

| Claim | Evidence |
|---|---|
| One visibility predicate governs every read; pending and rejected versions are owner-only; private and missing rigs are indistinguishable (404) | `TestVisibilityPredicate`, `TestOwnerSeesPendingAndRejectedVersionsOthersDoNot`, `TestPagesShowPublicRigsAndKeepPrivateOnesPrivate` |
| A removed version or rig is gone for everyone but admins; yanked versions still pull by exact version, with a marker | `TestResolveRangesAndYankAndRemove`, `TestReportFlowAndTakedown` |
| Reports cannot be used to probe for private rigs | `TestReportFlowAndTakedown` (private and missing give the same answer) |
| No stored XSS: README, manifest, description, file names and file contents are escaped or sanitised; raw file views are `text/plain` with `nosniff` and a `sandbox` CSP; CSP has no script source at all | `TestPagesEscapeHostileContent`, `TestSecurityHeaders` |
| Search input is data, never SQL | `TestSearchListsOnlyPublishedPublicRigs` (LIKE metacharacters, an injection string), `TestNoSQLIsBuiltFromStrings` (all queries are constants with placeholders) |

Known weak points:
- Search uses `LIKE` on unindexed columns: fine for thousands of rigs, not for millions. A performance limit, not a security one.
- The visibility predicate is SQL text repeated in a few queries (`rigVisibleAdm`, `versionVisible`). A new query that forgets it would leak. Review any new store method against it; a cheap follow-up is to route every version read through one function.

## 4. Abuse resistance and operations

| Claim | Evidence |
|---|---|
| Rate limits on login, device start and poll, code entry, search, resolve, download, upload and reports | `TestRateLimits`; limits are per process |
| Security headers on every response; HSTS over HTTPS | `TestSecurityHeaders`, E2E header check |
| Logs contain method, path (no query string), status and timing; never tokens, cookies or bodies | code review of `logged`; no test asserts absence, a reviewer should grep for other `Log.` calls |
| Every privileged action is audited (upload, yank, visibility, takedown, token issue/revoke, report handling, operator tools) | `TestReportFlowAndTakedown`, `TestAdminTools`, `TestResolveRangesAndYankAndRemove` |
| The service refuses to start with an insecure public URL, and without database, blob store or GitHub credentials | `TestConfigValidation`, `TestServeRefusesAnInsecureConfiguration` |
| Container runs as an unprivileged user; static binary | `Dockerfile.registry` |

Known weak points:
- **Rate limits are in memory per instance.** Behind several instances an attacker gets N times the budget. Fix before running more than one instance: a shared limiter (database or Redis).
- The client address for limiting is the socket peer unless `RIGFILE_REGISTRY_TRUST_PROXY=1`, in which case the *last* `X-Forwarded-For` entry is used: correct only behind exactly one trusted proxy that appends the peer. Misconfigured, everyone shares one budget or a client can pick its own.
- No email or paging on new reports or on scan failures: the operator must look (`rigfile-registry admin reports`).
- No backup, restore or retention procedure is provided; the database and bucket hold the only copy.
- Legal pages are drafts (`docs/owner-checklist.md`).

## 5. What an external reviewer should attack first

1. **`internal/registry/auth.go` and `store_auth.go`**: OAuth callback, session and token handling, device flow, especially the interaction between `DecideDevice` and `PollDevice` under concurrency.
2. **`internal/registry/upload.go`, `api.go` (`apiUpload`) and `internal/source/extract.go`**: everything between a hostile byte stream and the database.
3. **`internal/registry/worker.go` and `internal/publish` (`AuditDir`)**: whether any path can turn a rejected or pending version into a readable one, and whether the scanner can be starved or bypassed.
4. **`store_rigs.go`**: the visibility predicate in every query.
5. **`pages.go`, `markdown.go`, templates**: any output path that is not auto-escaped.
6. The deployment: TLS termination, proxy headers, database and bucket credentials, backups, the operator tools.

## 6. Stage 6 additions (2026-09-26)

New claims and their evidence:

| Claim | Evidence |
|---|---|
| Hostile text never reaches the page through analysis, similar-name or advisory data (fixed messages, bounded and printable-only third-party text) | `TestFindingsNeverEchoTheFile`, `TestPackageLookups` (hostile OSV answer) |
| A held or unreleased version is invisible to everyone but its owner and admins and is never resolved by a range | `TestHeldVersions` |
| Admin actions need an administrator session, same-origin and CSRF; a non-admin cannot tell `/admin` exists | `TestAdminPage` |
| Going public is gated on the newest version's analysis and on name look-alikes, and files exactly one review request | `TestHeldVersions`, `TestSimilarNamesAreRecordedShownAndHoldTheNameBack` |
| Signatures are verified before a version is accepted; a stranger's valid signature is never shown as the publisher's | `TestSignedUploadsAreVerifiedStoredAndShown`; cryptography in `internal/sigverify` tests, including a real public-good bundle |
| The pulling machine re-verifies, `--require-signature` refuses, a changed signer is refused on update | `TestSignedRigsAreVerifiedOnThePullingMachineAndSignerChangesAreRefused` |
| A popular rig cannot ship an unsigned or unverified-publisher version | `TestPopularRigPolicy` |
| A package listed as malicious rejects; an unreachable lookup is reported, never silently clean | `TestPackageLookupsDuringTheScan` |
| Incident switches work and are audited | `TestPublishingPauseAndTokenRevocation`, `TestAdminTools` |

New known weak points:
- **The analysis and similar-name rules are heuristics with a self-written corpus** (`docs/analysis-metrics.md`). Their value is raising the cost of obvious attacks and giving readers facts; they will miss determined attackers and will sometimes cry wolf. Held-for-review depends on an administrator actually looking at `/admin`.
- **The Sigstore trusted root is fetched over the network at first use** (TUF, cached) unless `RIGFILE_REGISTRY_SIGSTORE_ROOT` pins a file. That path is not exercised by automated tests (only the virtual Sigstore and a bundled real public-good bundle are). If the fetch fails, signed uploads answer 503 and pulls report "present but NOT verified here".
- **Only GitHub Actions identities count as the publisher.** Interactive keyless signatures verify but are shown as not-the-publisher.
- **Star counts can be bought or faked**; the popular-rig policy is a soft trigger.
- **OSV is a third party**: its outage degrades the check to a warning; its content is treated as untrusted text. The `MAL-` convention is unverified against the live API.
- **No re-scan of stored versions** when rules improve.

## 7. Stage 8 additions (2026-09-26)

New surface: version diffs, forks (derived rigs), collections, organisations, and (client side) `rigfile ui`.

| Claim | Evidence |
|---|---|
| A diff never shows a viewer a version they cannot see; a cached result cannot widen that; hostile text is escaped; diffs are rate limited and bounded | `TestDiffBetweenVersions` (pending and private versions, escaping, rate limit), `docs/diffs.md` |
| Derived-rig lists and collections reveal only what search already shows / what the viewer may see | `TestDerivedRigsAndUseAsBase`, `TestCollections` |
| **Organisation membership is the access control for its rigs, on every read path**, and a departed member loses access at once | `TestOrganisations` (a stranger, a former member and an anonymous viewer against every route, plus collections, diffs and search) |
| A person and an organisation cannot hold one name, even racing | `TestNamespaceRaceHasOneWinner`, migration `0004` triggers |
| Role limits: admins cannot touch owners; the last owner stays | `TestOrganisations` |
| A disabled organisation's rigs vanish for everyone but site admins | `TestDisabledOrganisationVanishes` |

Attack these first: the two SQL fragments in `store_rigs.go` (`rigVisibleAdm`, `versionVisible`) now embed `rigMember` and the disabled-organisation clause, so a mistake there is a leak across every feature; `CreateVersion` (which now decides organisation membership inside the upload transaction); `store_orgs.go` role checks; the `namespace_free()` trigger; and `internal/registry/diff.go` (two archive extractions per request).

New known weak points:
- An organisation member is fully trusted with the organisation's namespace: any member can publish (and yank) a version that other members' machines will pull. There is no per-rig role and no approval step.
- Invitations do not exist: members must have signed in once. Organisation creation is open to any signed-in user (capped at 10 each); squatting is handled by admin `disable-org`, not prevented.
- Diff notes are a fixed rule list, not analysis: a malicious change in a file the rules do not recognise is shown as a plain text diff without a note.
