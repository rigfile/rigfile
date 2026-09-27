# Stage 5 owner checks (S5-M7)

The registry is built and tested here against a real Postgres, a fake GitHub and a local disk bucket. Everything below needs your accounts, money, a lawyer or other people.

## 1. Push and read CI

`git push -u origin stage-5`. New jobs: `registry (postgres)` (service container) and `registry e2e (docker compose)`. Also the `windows-latest` and macOS test jobs now compile the registry packages (their tests skip without a database). Send me any red log.

## 2. Decide

| Decision | Notes |
|---|---|
| ~~**Domain and hosting**~~ | Decided/done 2026-09-27: Fly.io (managed platform, one app, two machines, `auto_stop_machines`), Neon Postgres (point-in-time restore), Cloudflare R2. Domain `rigfile.bytebuilderslab.app`, CNAME to the Fly `.fly.dev` hostname per Fly's own guidance for subdomains. Live at `https://rigfile.bytebuilderslab.app`. |
| ~~**Bucket**~~ | Decided/done 2026-09-27: Cloudflare R2, real endpoint (`<account-id>.r2.cloudflarestorage.com`), scoped API token. Publish → scan → blob write confirmed working against the real bucket. |
| ~~**GitHub sign-in**~~ | Decided/done 2026-09-26, **tested live 2026-09-27**: OAuth App under the `rigfile` org, homepage and callback `https://rigfile.bytebuilderslab.app` / `/auth/callback`, wildcard matching off, device flow off (Rigfile has its own). `RIGFILE_REGISTRY_GITHUB_CLIENT_ID=Ov23licYmVqaKBqv1rNb`; `..._CLIENT_SECRET` set via `fly secrets set` by the owner directly, never pasted to me. No scopes requested. A real `rigfile login` against the real deployment worked. |
| ~~**Admins**~~ | Decided 2026-09-26: sole admin while in staging, checked on-demand (a report or takedown request), not on a schedule. `RIGFILE_REGISTRY_ADMINS=digitaldreamer3462`. Revisit before public launch or when a second person needs `/admin` — there is still no alerting (§6 below), so a report is only seen by running `rigfile-registry admin reports`. |
| ~~**Retention of rejected uploads**~~ | Decided 2026-09-26: delete the archive at once (docs/registry-security.md §2); the rejection reason stays on the version row so the uploader still sees why. |
| ~~**GitHub rename hijack**~~ | Decided 2026-09-26: reserve vacated logins that own rigs (built; docs/registry-security.md §1). Back up the database before migration 0005 as well. |
| **Multi-instance** | The rate limiter is per process. Run one instance until a shared limiter exists. |

## 3. First deployment (staging first) — **done 2026-09-27**

1. ~~Provision Postgres 15+ and the bucket~~ — Neon Postgres + Cloudflare R2, credentials set via `fly secrets set`, never in the image or the repo.
2. ~~Run the image behind a TLS-terminating proxy~~ — Fly.io terminates TLS; `RIGFILE_REGISTRY_PUBLIC_URL=https://rigfile.bytebuilderslab.app`; `RIGFILE_REGISTRY_TRUST_PROXY=1` set (Fly's proxy appends the real client address as the last `X-Forwarded-For` entry).
3. ~~`rigfile-registry migrate`; `GET /healthz`~~ — migrations run automatically on start (the Dockerfile's `CMD ["serve"]`); `/healthz` returns healthy, TLS verified.
4. ~~Sign in with GitHub; publish; pull~~ — real `rigfile login` and real `rigfile publish <dir> --to-registry` both done against the live deployment (see `docs/STATUS.md` "Registry: live deployment" for the one real finding: the pin-check blocks *any* registry upload, private or public, not only `--public` ones). **Still open: pulling a published rig from a second real machine/account** — not yet exercised against the live deployment (the container E2E already proves the mechanism).
5. Back up the database and the bucket; test a restore. **Still open** — no restore has been rehearsed yet, and no procedure is provided by the software.
6. Watch `rigfile-registry admin reports` and `admin audit` (there is no email or alerting). **Still open** — not yet exercised against the live deployment.

## 4. Legal

`/legal/terms`, `/legal/acceptable-use`, `/legal/takedown` are **drafts with bracketed placeholders** and a visible "not reviewed by a lawyer" banner. Before opening to the public: have a lawyer write or review them for your jurisdiction (liability, DMCA-style notices, privacy, children, governing law), fill the placeholders (operator name, contacts, response times), and remove the banner. A **privacy policy** does not exist yet: the service stores GitHub id, login, name and avatar URL, IP-derived rate-limit state (in memory only) and an audit log.

## 5. Abuse handling

Decide who reads reports, how fast, and what your escalation is for malware and for legal notices. The tools are `rigfile-registry admin reports | resolve-report | takedown | disable-user | audit`.

## 6. External security review (exit criterion)

`docs/registry-security.md` §5 lists what to attack first. The exit criterion says *security review of auth, upload and scanning complete*: my self-review does not satisfy it. Options: a paid audit, or a scoped bug-bounty/"friends and family" review before launch. Give the reviewer the repository, `docs/registry.md` and `docs/registry-security.md`, and a staging deployment with an admin account.

## 7. Exit criteria (plan §12, Stage 5)

| Criterion | Evidence today | Open |
|---|---|---|
| `rigfile publish` → page appears after the scan → another user `rigfile pull owner/name` works | container E2E (`e2e/registry.sh`: real image, Postgres, two machines) and `TestRegistryEndToEndPublishThenPullOnAnotherMachine`; **`rigfile login` + `rigfile publish --to-registry` done live 2026-09-27 against the real deployment**, page confirmed | pulling the published rig from a second real account/machine against the live deployment |
| Security review of auth, upload and scanning complete | self-review with tests and `govulncheck` | **external review (§6)** |
| Terms, acceptable-use policy, takedown process | drafts and working tools | lawyer (§4) |
