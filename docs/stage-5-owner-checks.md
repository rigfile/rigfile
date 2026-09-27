# Stage 5 owner checks (S5-M7)

The registry is built and tested here against a real Postgres, a fake GitHub and a local disk bucket. Everything below needs your accounts, money, a lawyer or other people.

## 1. Push and read CI

`git push -u origin stage-5`. New jobs: `registry (postgres)` (service container) and `registry e2e (docker compose)`. Also the `windows-latest` and macOS test jobs now compile the registry packages (their tests skip without a database). Send me any red log.

## 2. Decide

| Decision | Notes |
|---|---|
| **Domain and hosting** | The CLI has no default registry URL until you have one (`RIGFILE_REGISTRY`, `--registry`). Where does it run (a VM with Docker, Fly.io, Render, Cloud Run, Kubernetes)? It is one stateless container plus Postgres plus a bucket. |
| **Bucket** | Cloudflare R2 is what the plan suggests; any S3-compatible bucket works (`RIGFILE_REGISTRY_BLOB=s3` and the `RIGFILE_REGISTRY_S3_*` variables). Not tested against a real R2 endpoint: only against a fake S3 server. |
| **GitHub sign-in** | Create an OAuth App (Settings > Developer settings): callback `https://<domain>/auth/callback`. Set `RIGFILE_REGISTRY_GITHUB_CLIENT_ID` and `..._CLIENT_SECRET` (or `..._SECRET_FILE`). No scopes are requested. **Never tested against the real github.com** (`GitHubHTTP` is tested against a fake): do a real sign-in on a staging deployment. |
| **Admins** | `RIGFILE_REGISTRY_ADMINS=login1,login2` (GitHub logins). |
| ~~**Retention of rejected uploads**~~ | Decided 2026-09-26: delete the archive at once (docs/registry-security.md §2); the rejection reason stays on the version row so the uploader still sees why. |
| ~~**GitHub rename hijack**~~ | Decided 2026-09-26: reserve vacated logins that own rigs (built; docs/registry-security.md §1). Back up the database before migration 0005 as well. |
| **Multi-instance** | The rate limiter is per process. Run one instance until a shared limiter exists. |

## 3. First deployment (staging first)

1. Provision Postgres 15+ and the bucket; keep credentials in the platform's secret store, never in the image.
2. Run the image (`Dockerfile.registry`) behind a TLS-terminating proxy. Set `RIGFILE_REGISTRY_PUBLIC_URL=https://<domain>`. If the proxy is the only thing that can reach the container, set `RIGFILE_REGISTRY_TRUST_PROXY=1` **only if** it appends the real client address as the last `X-Forwarded-For` entry.
3. `rigfile-registry migrate` (also runs at start). `GET /healthz`.
4. Sign in with GitHub in a browser; run `rigfile login --registry https://<domain>`; publish a rig (`rigfile publish <dir> --to-registry`); pull it from another machine.
5. Back up the database and the bucket; test a restore. **No procedure is provided by the software.**
6. Watch `rigfile-registry admin reports` and `admin audit` (there is no email or alerting).

## 4. Legal

`/legal/terms`, `/legal/acceptable-use`, `/legal/takedown` are **drafts with bracketed placeholders** and a visible "not reviewed by a lawyer" banner. Before opening to the public: have a lawyer write or review them for your jurisdiction (liability, DMCA-style notices, privacy, children, governing law), fill the placeholders (operator name, contacts, response times), and remove the banner. A **privacy policy** does not exist yet: the service stores GitHub id, login, name and avatar URL, IP-derived rate-limit state (in memory only) and an audit log.

## 5. Abuse handling

Decide who reads reports, how fast, and what your escalation is for malware and for legal notices. The tools are `rigfile-registry admin reports | resolve-report | takedown | disable-user | audit`.

## 6. External security review (exit criterion)

`docs/registry-security.md` §5 lists what to attack first. The exit criterion says *security review of auth, upload and scanning complete*: my self-review does not satisfy it. Options: a paid audit, or a scoped bug-bounty/"friends and family" review before launch. Give the reviewer the repository, `docs/registry.md` and `docs/registry-security.md`, and a staging deployment with an admin account.

## 7. Exit criteria (plan §12, Stage 5)

| Criterion | Evidence today | Open |
|---|---|---|
| `rigfile publish` → page appears after the scan → another user `rigfile pull owner/name` works | container E2E (`e2e/registry.sh`: real image, Postgres, two machines) and `TestRegistryEndToEndPublishThenPullOnAnotherMachine` | the same on a real deployment with real GitHub sign-in |
| Security review of auth, upload and scanning complete | self-review with tests and `govulncheck` | **external review (§6)** |
| Terms, acceptable-use policy, takedown process | drafts and working tools | lawyer (§4) |
