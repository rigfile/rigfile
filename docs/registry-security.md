# Registry security self-review (S5-M6)

**This is my review of my own work. It is not the external security review the Stage 5 exit criterion requires** (`docs/stage-5-owner-checks.md` §6). Its job is to hand a reviewer a map: what protects what, where the evidence is, and what is known to be weak. Date: 2026-09-26, branch `stage-5`.

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
- **GitHub login reuse.** The account follows the numeric GitHub id, and rigs are keyed by the login string at creation time. If a person renames their GitHub account and someone else takes the old name, the newcomer could publish *new* rig names under the old owner string (existing rigs stay with their creator: ownership is checked by `created_by`, not by name). Mitigation to consider: reserve vacated logins for a period, or key namespaces by id. **Open.**
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

Known weak points:
- The scan covers secrets, manifest validity and pinning. **It does not analyse hooks or scripts for malicious behaviour, and does not check package reputation or typosquatting** (Stage 6). A rig can pass the scan and still be harmful; the pull screen and the plan screen are the defence today, and the site says so.
- Scanning happens after the upload is stored. Rejected blobs remain in the bucket (they may contain a secret the publisher pasted). They are unreachable through the API (the visibility predicate) but exist on disk. **Decision needed:** delete rejected blobs after N days.
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
- Legal pages are drafts (`docs/stage-5-owner-checks.md` §4).

## 5. What an external reviewer should attack first

1. **`internal/registry/auth.go` and `store_auth.go`**: OAuth callback, session and token handling, device flow, especially the interaction between `DecideDevice` and `PollDevice` under concurrency.
2. **`internal/registry/upload.go`, `api.go` (`apiUpload`) and `internal/source/extract.go`**: everything between a hostile byte stream and the database.
3. **`internal/registry/worker.go` and `internal/publish` (`AuditDir`)**: whether any path can turn a rejected or pending version into a readable one, and whether the scanner can be starved or bypassed.
4. **`store_rigs.go`**: the visibility predicate in every query.
5. **`pages.go`, `markdown.go`, templates**: any output path that is not auto-escaped.
6. The deployment: TLS termination, proxy headers, database and bucket credentials, backups, the operator tools.
