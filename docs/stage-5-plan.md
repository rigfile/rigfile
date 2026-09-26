# Stage 5 plan of record: registry website MVP

**Goal (RIGFILE_PLAN.md §12):** the community site. `rigfile publish` puts a rig in a registry, a scan gates its visibility, the rig gets a page, and another person runs `rigfile pull owner/name`.
**Exit:** end to end `rigfile publish` → page appears after the scan → another user `rigfile pull owner/name` works; security review of auth, upload and scanning complete.

Started 2026-09-26 on branch `stage-5`; S5-M0 to S5-M6 built the same day (Stages 1-4 merged to `main`, CI green). Spec, API and threat model: `docs/registry.md`.

**What I can and cannot do.** I can build and test the whole service locally and in CI: API, database, blob store, authentication flows (against a fake GitHub), scan worker, web pages, CLI integration, container E2E with a real Postgres. I cannot: register a domain or hosting, create the GitHub OAuth app, provision Cloudflare R2, get a legal review of the Terms, commission the external security review, or find users. Those are S5-M7 (owner). The security *self*-review is mine (S5-M6); it does not replace the external one the exit criterion asks for.

## Design calls (decided by my recommendation; the plan's §10.2 said "suggested")

1. **One Go service instead of Next.js + a separate API.** The server-side scan must be *the same code* as the local one (`internal/scan`, `manifest`, the hardened extractor in `internal/source`, `internal/publish`); in Go that is a package import, not a second implementation that can disagree. Pages are server-rendered (`html/template`, no client framework, no inline script), which also keeps the security surface small. The plan's stack stays possible later as a separate front end over the same JSON API.
2. **Postgres (pgx) and an S3-compatible blob store behind an interface** (filesystem for dev and self-hosting, S3/R2 through `minio-go`). Blobs are content-addressed and immutable.
3. **Auth:** GitHub OAuth for the web, an RFC 8628 device flow for the CLI (`rigfile login`, using the flow already built in `internal/login`). The registry issues its own opaque tokens, stored **hashed**; the CLI keeps its token in the OS keychain through the secret store. Tokens expire in 30 days and are revocable (`rigfile logout` revokes server-side).
4. **Namespace = the GitHub login** (lowercased); organisations come later. `rigfile/*` and vendor names are reserved (admins only).
5. **Private by default.** A private rig is scanned and only warns; making it public requires the scan to pass. Only the owner sees private rigs.
6. **Publish is asynchronous:** upload validates and stores, the version is `pending`, a DB-backed worker scans it (secrets, schema, pinning; hooks/scripts static analysis is Stage 6), then it becomes `published` or `rejected` with findings the owner can read. Nothing pending or rejected is visible to anyone else or pullable.
7. **Immutability:** a published `owner/name@version` never changes. `yank` hides it from resolution but exact-version pulls (and lockfiles) still work, with a warning; `remove` (takedown for malware/abuse/legal) makes it unavailable.
8. **No default registry URL** until the owner registers a domain: `RIGFILE_REGISTRY` or `--registry`.
9. **Legal pages are drafts** (Terms, Acceptable Use, takedown/abuse policy) that a lawyer must review before launch.

## Milestones

| # | Milestone | Delivers | Acceptance | Status |
|---|---|---|---|---|
| S5-M0 | **Spec** | `docs/registry.md`: data model, API, auth flows, rules, threat model, abuse handling | Every endpoint and rule has a named test | **done**: `docs/registry.md` |
| S5-M1 | **Foundation** | `internal/registry` skeleton, config, Postgres schema + migrations, blob store (fs + S3), health, structured logs | Integration tests on a real Postgres (Docker locally, service container in CI); blob store contract tests | **done**: Postgres schema and embedded migrations, fs and S3 blob stores (S3 tested against a fake server only), per-test schemas, `scripts/registry-test.sh` |
| S5-M2 | **Auth** | GitHub OAuth (web, against a fake GitHub in tests), sessions, CSRF, API tokens, device flow endpoints, rate limits, security headers | Forged state/CSRF/expired/revoked token cases rejected; tokens never stored in clear | **done**: GitHub OAuth (fake GitHub in tests), sessions, CSRF, API tokens, device flow, rate limits, security headers |
| S5-M3 | **Publish and pull API** | upload with hardened extraction, immutability, reserved names, scan worker, visibility, yank/remove, resolve by range, tarball/manifest/blob endpoints | Malicious archives, secrets, unpinned, name/owner mismatch, duplicate version, private access all tested | **done**: upload with hardened extraction, immutability, reserved names, scan worker (SKIP LOCKED), one visibility predicate, yank/remove, resolve, search, tarball/manifest endpoints |
| S5-M4 | **CLI** | `rigfile login/logout`, `publish --to-registry [--public]`, `pull owner/name[@range]`, registry-backed `from:` layers; lock pins the tarball hash | End-to-end test with an in-process registry: publish → pending → published → pull on a second machine | **done**: `internal/regclient`, `rigfile login/logout/whoami`, `publish --to-registry [--public]`, `pull owner/name[@range]`, registry `from:` layers pinned in the lock, `update` |
| S5-M5 | **Web UI** | home, search, profile, rig page (README, manifest, layers, files, targets, secrets/logins needed, install command, versions), stars, sign in/out | Rendering and XSS tests (README, manifest and file contents are hostile input); CSP without inline script | **done**: home, search, profile, rig page (README, manifest, files, versions, targets, needs, install command), stars, visibility, raw text view; hostile-content tests |
| S5-M6 | **Abuse, legal, ops** | report + takedown flow and admin commands, draft Terms/AUP pages, audit log, Dockerfile + compose, CI job, container E2E, security self-review (`docs/registry-security.md`) | E2E on Ubuntu with Postgres; every self-review item has a test or an explicit owner action | **done**: reports and takedown (`rigfile-registry admin ...`), draft legal pages, audit log, `rigfile-registry serve/migrate`, Dockerfile, compose, CI jobs, container E2E, `docs/registry-security.md` |
| S5-M7 | **Owner gate** | `docs/stage-5-owner-checks.md`: domain and hosting, GitHub OAuth app, R2 bucket, secrets management, legal review, external security review, first real users | Exit criteria above | todo (owner) |
