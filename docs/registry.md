# The Rigfile registry: spec, API and threat model (S5-M0)

Status: spec of record for Stage 5, implemented in `internal/registry` and `cmd/rigfile-registry`. A rule without a test is a bug in this document; tests are named in the last column of §6.

## 1. What it is

A web service where people publish rigs, browse them, star them, and pull them with `rigfile pull owner/name`. It stores **rigs only**: never a user's secrets (a rig cannot contain values by schema and by scan). It is one Go binary plus Postgres plus an S3-compatible bucket.

## 2. Data model

| Table | Holds |
|---|---|
| `users` | `id`, `github_id` (unique), `login` (lowercased, unique), display name, avatar URL, `is_admin`, `disabled_at` |
| `sessions` | web sessions: `id_hash` (SHA-256 of the cookie value), `user_id`, `csrf_secret`, `expires_at` |
| `api_tokens` | CLI tokens: `token_hash`, `user_id`, `name`, `expires_at`, `revoked_at`, `last_used_at` (the token itself is shown once, never stored) |
| `device_codes` | `device_code_hash`, `user_code`, `user_id` (set on approval), `status` (`pending`/`approved`/`denied`), `expires_at`, `interval_s`, `last_poll_at` |
| `rigs` | `owner`, `name` (unique together), `description`, `visibility` (`private`/`public`), `created_by`, `removed_at` |
| `versions` | `rig_id`, `version` (unique with rig), `status` (`pending`/`published`/`rejected`/`yanked`/`removed`), `tarball_sha256`, `size`, `manifest_json`, `readme`, `targets`, `needs_secrets`, `needs_logins`, `layers`, `scan_findings` (JSON, never containing values), `created_at`, `scanned_at`, `yanked_at`, `yank_reason` |
| `version_files` | `version_id`, `path`, `size`, `sha256`, `is_text` (the file index the page shows) |
| `stars` | `user_id`, `rig_id` |
| `jobs` | scan queue: `version_id`, `state`, `attempts`, `run_after`, `locked_by`, `locked_until` |
| `reports` | abuse reports: `rig_id`, `version`, `reporter_id` (nullable), `reason`, `details`, `status` |
| `audit_log` | who did what: publish, yank, remove, visibility change, login, token issue/revoke, admin actions |

Blobs: `blobs/sha256/<aa>/<hex>` (tarballs), content-addressed and immutable; the database row is the only thing that makes a blob reachable.

**The visibility predicate** is defined once (`store.Visible`) and used by every read: a version is readable by a viewer iff it is `published` or `yanked` AND (the rig is public OR the viewer owns it) AND neither the rig nor the version is `removed`; owners additionally see their own `pending` and `rejected` versions. There is no code path that reads a version without going through it.

## 3. API

JSON unless stated. Errors are `{"error": "..."}` with a stable HTTP status. A private or missing rig is always **404** (never 403), so private names cannot be enumerated.

| Method and path | Auth | Purpose |
|---|---|---|
| `GET /healthz` | none | liveness and database reachability |
| `GET /v1/search?q=&limit=` | optional | public rigs matching name, description, owner |
| `GET /v1/rigs/{owner}/{name}` | optional | metadata, visibility, stars, versions the viewer may see |
| `GET /v1/rigs/{owner}/{name}/resolve?range=^1.2` | optional | the newest non-yanked version satisfying the range (`layers.Satisfies` rules); exact versions may be yanked |
| `GET /v1/rigs/{owner}/{name}/diff?from=&to=` | optional | what changed between two versions the viewer may see (`from` defaults to the version before `to`, `to` to the newest published): manifest items added, removed, changed, file changes with unified text diffs, and the notes that ask for review (S8-M1, `docs/diffs.md`) |
| `GET /v1/rigs/{owner}/{name}/versions/{v}` | optional | version detail; scan findings only for the owner |
| `GET /v1/rigs/{owner}/{name}/versions/{v}/manifest` | optional | `rigfile.yaml` |
| `GET /v1/rigs/{owner}/{name}/versions/{v}/tarball` | optional | the gzip tarball; headers `X-Rigfile-SHA256`, `ETag`, immutable caching |
| `POST /v1/rigs/{owner}/{name}/versions` | token | upload a version (`Content-Type: application/gzip`, body = tarball); creates the rig private on first upload; `202 {version, status:"pending"}` |
| `POST /v1/rigs/{owner}/{name}/versions/{v}/yank` | token (owner) | yank with a reason |
| `POST /v1/rigs/{owner}/{name}/visibility` | token (owner) | `{"visibility":"public"}` requires the latest version to be scan-clean |
| `PUT`/`DELETE /v1/rigs/{owner}/{name}/star` | token | star and unstar |
| `GET /v1/me` | token | who am I |
| `DELETE /v1/tokens/current` | token | revoke this token (`rigfile logout`) |
| `POST /v1/device/code` | none | start a device sign-in (RFC 8628): `device_code`, `user_code`, `verification_uri`, `interval`, `expires_in` |
| `POST /v1/device/token` | none | poll: the token, or `authorization_pending` / `slow_down` / `access_denied` / `expired_token` |

Deviation from the plan's sketch: the content API is the tarball endpoint (a rig is one immutable blob); a generic `/blobs/:sha256` endpoint would be a second path to the same bytes that would have to repeat the visibility check, so it is not offered.

Web pages (server-rendered, no inline script): `/`, `/search`, `/u/{login}`, `/r/{owner}/{name}`, `/r/{owner}/{name}/v/{version}`, `/r/{owner}/{name}/v/{version}/files/{path}`, `/r/{owner}/{name}/diff`, `/login`, `/auth/callback`, `/logout`, `/device`, `/report`, `/legal/terms`, `/legal/acceptable-use`, `/legal/takedown`.

## 4. Auth

- **Web:** `/login` redirects to GitHub's authorize URL with a random `state` bound to a short-lived cookie; `/auth/callback` checks `state` in constant time, exchanges the code, reads the GitHub user id and login, upserts the user, and starts a session. Session cookie: 32 random bytes, stored hashed, `HttpOnly`, `Secure`, `SameSite=Lax`, 7 days. Every state-changing web request needs a per-session CSRF token and a same-origin `Origin`/`Referer`.
- **CLI:** the device flow. `rigfile login` calls `/v1/device/code`, shows the user code and address, and polls `/v1/device/token`. The user opens `/device` (signing in with GitHub if needed), types the code, and approves. The token returned is opaque, 32 random bytes, stored hashed, expires in 30 days and can be revoked. `slow_down` and per-code rate limits are enforced server-side.
- **Namespace:** a token may publish only under `owner == its user's login`, unless the user is an admin. `rigfile/*` and vendor names (`anthropic`, `openai`, `google`, `claude`, `codex`, `gemini`, `cursor`, `github`, `microsoft`, `rigfile`) are reserved for admins.
- Accounts can be disabled by an admin; a disabled user's sessions and tokens stop working and their public rigs become unlisted.

## 5. Publish, scan, visibility

1. **Upload** (token, `application/gzip`): body capped at 20 MiB; unpacked with the hardened extractor (`source.Extract`: no traversal, links, devices, case collisions, reserved names, entry/size limits) into a temporary directory; `manifest.Load` must succeed and `manifest.Check` must have no errors; the manifest `name` must equal `{owner}/{name}` of the URL and the `version` the URL's; `rigfile/*` and layers named `rigfile/*` are refused; a duplicate `owner/name@version` is a 409 (unique constraint, no race). The tarball is stored by hash, the version row is `pending`, a scan job is queued. Response `202`.
2. **Scan worker** (in the same process or `rigfile-registry worker`): takes jobs with `FOR UPDATE SKIP LOCKED`, time-boxed; runs the full secret scanner over every file and file name, `manifest.Check`, the pinning rule, and stores findings without values. Clean: `published`. Findings on a **public** rig, or a secret finding on any rig: `rejected` (a private rig with only warnings such as unpinned packages is published with the warnings). A crashed worker's job is picked up again after `locked_until`.
3. **Visibility** changes to `public` only if the newest published version has no findings; new versions of a public rig are scanned before they can be pulled as "latest".
4. **Yank** hides a version from `resolve` and listings' "latest" but leaves exact pulls working (lockfiles keep working) with a `yanked` marker the CLI prints. **Remove** (takedown) makes it unavailable to everyone but admins; the blob is kept for the audit trail.

## 6. Rules and their tests

| Rule | Test |
|---|---|
| Hostile archives are refused (traversal, symlink, bomb, too large, bad manifest) | `TestUploadRefusesHostileArchives` |
| Name/version in the manifest must match the URL; owner must match the token's login | `TestUploadNamespaceRules` |
| Reserved owners and names need an admin | `TestReservedNames` |
| A version is immutable; a duplicate is 409 even under concurrent uploads | `TestVersionsAreImmutable` |
| Pending and rejected versions are invisible to everyone but the owner | `TestVisibilityPredicate` |
| A secret in any file rejects the version; the findings never contain the value | `TestScanRejectsSecretsWithoutLeakingThem` |
| Public rigs must pin; private ones only warn | `TestPinningPublicVersusPrivate` |
| Private and missing rigs are indistinguishable (404) | `TestVisibilityPredicate` (private and missing are byte-identical), `TestPagesShowPublicRigsAndKeepPrivateOnesPrivate` |
| Tokens and sessions are stored hashed, expire, and can be revoked | `TestTokensAreHashedExpireAndRevoke` |
| OAuth `state` mismatch, replay and missing cookie are refused | `TestOAuthStateIsChecked` |
| Device flow: pending, slow_down, approval, denial, expiry, reuse of a used code | `TestDeviceFlow` |
| State-changing web requests need CSRF and same-origin | `TestCSRF` |
| README, manifest and file contents cannot inject script | `TestPagesEscapeHostileContent` |
| Security headers on every response, CSP without inline script | `TestSecurityHeaders` |
| Rate limits on auth, device polling and upload | `TestRateLimits` |
| A yanked version still pulls by exact version; a removed one does not | `TestResolveRangesAndYankAndRemove`, `TestReportFlowAndTakedown` |
| The worker survives a crash and does not double-scan | `TestWorkerRecoversAndLocks` |

## 7. Threat model (registry-specific; the general one is RIGFILE_PLAN.md §11)

| Threat | Mitigation |
|---|---|
| Zip/tar bombs, path traversal, links in uploads | the hardened extractor and size caps (Stage 4 code, same tests) |
| A publisher uploads a secret | local scrub blocks it; the server re-scans with the same scanner; findings never contain values; a rejected blob is not readable by anyone but the owner's findings view |
| Stored XSS through README, manifest, instructions, file browser, rig name | `html/template` auto-escaping everywhere; README rendered with raw HTML **disabled** and links restricted to `http(s)`, `mailto` and relative; file contents shown as escaped text with `Content-Type: text/plain` on raw views and `X-Content-Type-Options: nosniff`; CSP `default-src 'none'; style-src 'self'; img-src 'self' https://avatars.githubusercontent.com; form-action 'self'; frame-ancestors 'none'` |
| CSRF | SameSite=Lax cookies, per-session CSRF token on every state-changing form, Origin check; the API uses bearer tokens (not cookies), so it is not CSRF-able |
| OAuth login CSRF / open redirect | random `state` bound to a cookie, checked constant-time; the post-login redirect is a same-site relative path only |
| Token theft from the database | tokens and session ids are stored as SHA-256 hashes; 30-day expiry; revocable; `last_used_at` recorded |
| Enumerating private rigs | 404 for both "private" and "missing"; identical response timing is not promised beyond that |
| Squatting and impersonation | namespace = GitHub login; reserved names admin-only; typosquat detection is Stage 6 |
| Rug pull (a good rig turns bad) | immutable versions; the lock pins the tarball hash; updates go through a plan screen |
| SSRF | the server makes outbound requests only to GitHub (fixed, configurable base URLs); it never fetches user-supplied URLs |
| SQL injection | parameterised queries only (a test greps for string-built SQL) |
| DoS | body caps, extraction caps, per-IP and per-user rate limits, worker time boxes and bounded concurrency |
| Scan bypass by status confusion | one visibility predicate used by every read; status transitions only in the worker and the admin/owner actions, each audited |
| Abuse content (illegal, malware, harassment) | report button and email; admin `remove`; audit log; Terms and AUP (drafts) |
| Secrets in logs | structured logs carry ids and hashes, never tokens, cookies, upload bodies or file contents |
| Compromised registry serving a tampered tarball | the CLI checks the tarball hash recorded in the lock; signed rigs and verified publishers are Stage 6 |
| Account takeover of a popular publisher | 2FA policy for popular rigs is Stage 6; until then GitHub's own account security is the boundary and this is stated on the pull screen |

## 8. Operations (S5-M6)

Configuration is environment variables (`RIGFILE_REGISTRY_*`): listen address, public URL, database URL, blob backend (`fs:/path` or `s3://bucket` with endpoint and keys), GitHub OAuth client id and secret, session key, admin logins, rate-limit settings. Secrets are read from the environment or files, never from flags. Migrations run at start (`rigfile-registry migrate`). `docker compose up` runs the service, Postgres and (optionally) MinIO for local trials.

## 9. As built: differences from the sketch above

- **Reports** need a signed-in user (GitHub identity), are rate limited per user and address, and cannot be used to probe private rigs (`TestReportFlowAndTakedown`). Admins act through the operator tools, not through web pages: `rigfile-registry admin reports|resolve-report|takedown|disable-user|audit`.
- **Bootstrap and break-glass:** `admin create-user` and `admin token` make an account and a token without GitHub. They need shell access to the deployment and are audited; the container E2E uses them.
- **`DeviceInterval`** (default 5 s) is configurable so tests do not wait.
- **Layers from the registry:** `from: [owner/name@range]` resolves through the registry when no local layers directory has it, and is pinned in `rigfile.lock` (`source`, `commit` = tarball SHA-256, `treeSha256`).
- **Registry sources** are written `rigfile+https://host/owner/name[@range]` in `state.json` and the lock.
- **The CLI's token** lives in the secret store under `registry/<host>/token`. `RIGFILE_SECRETS_BACKEND=file` forces the encrypted-file backend (scripts and CI must set it: they must never reach a developer's OS keychain).
- **Not built (later):** organisations, forks ("use as base"), comments, download counts, verified publishers, typosquat detection (Stage 6), a shared rate limiter, email notifications.

## 10. Stage 6 additions (see `docs/trust.md`)

- **Statuses:** `held` joins the others; it is invisible except to the owner and admins, exactly like `pending`.
- **Endpoints:** `GET /v1/rigs/{o}/{n}/versions/{v}/trust` (facts), `.../bundle` (the stored Sigstore bundle); `POST /v1/rigs/{o}/{n}/versions` also accepts `multipart/form-data` with parts `tarball` and `bundle`. Version JSON carries `analysis`, `similar_to` and (to the owner) `held_reason`.
- **Web:** `/admin` for administrators (held versions, open reports, verify a publisher; session, same-origin and CSRF checked; a non-admin gets 404).
- **Operator tools:** `verify-publisher`, `unverify-publisher`, `held`, `release`, `reject`, `approve-public`, `publishing pause|resume`, `revoke-tokens`.
- **Uploads while paused** are refused with 503 and the reason.
