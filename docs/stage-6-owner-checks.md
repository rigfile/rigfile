# Stage 6 owner checks (S6-M8)

Stage 6 is built and tested against fakes and bundled test data. Nothing below has been run against real Sigstore, real OSV, or real people. The exit criteria are **an external security review passed** (covering Stages 5 and 6) and **an incident response runbook written** (it is written; it has never been rehearsed).

## 1. Push and read CI

`git push -u origin stage-6`. The `registry (postgres)` job runs everything new that needs a database; `test (*)` runs `internal/analyze`, `internal/similar`, `internal/sigverify`, `internal/pkgcheck` on all three OSes. Send me any red log.

## 2. Decisions

| Decision | Recommendation | Where it lands |
|---|---|---|
| **Popular threshold** (`RIGFILE_REGISTRY_POPULAR_STARS`) | leave 0 (off) until you have publishers who can sign; then 25-50 | configuration |
| **Who is a verified publisher** | people and organisations you can identify out of band (a domain, a known organisation, someone you know); record the kind and a private note | `rigfile-registry admin verify-publisher` |
| **Who reads `/admin`** and how often | a named person, at least daily while the registry is young; there is no alerting | runbook §1 |
| **Similar-name strictness** | the defaults are conservative (distance ≤ 1, ≤ 2 for long names; only notable rigs, 5+ stars or verified, trigger review); tune after real data | `internal/similar` |
| **OSV** | keep on; check its terms of use for your expected volume; pin `RIGFILE_REGISTRY_OSV_URL` if you mirror it | configuration |
| **Sigstore root** | fetch over TUF (default) on a staging deployment first; pin a file with `RIGFILE_REGISTRY_SIGSTORE_ROOT` if you need offline | configuration |

## 3. Live checks I could not run (each settles an UNVERIFIED)

| # | Check | How |
|---|---|---|
| 1 | The Sigstore trusted-root fetch (TUF, public-good instance) works from your deployment and from a laptop | `rigfile pull <a signed rig>` on a machine with network access; look for "verified on this machine" |
| 2 | A real keyless signature made in GitHub Actions verifies and is recognised as the publisher's | in a test repository: a workflow that builds `rig.tgz` with `rigfile publish --write-tarball` and signs it with `cosign sign-blob --bundle` (`id-token: write` permission), then upload with `--sign-bundle` under the same GitHub login |
| 3 | OSV: the request shape (confirmed from the documentation), the `npm` and `PyPI` ecosystem strings, and that malicious packages appear with `MAL-` ids | query a known-malicious package from OpenSSF's malicious-packages data with `curl -d '{"package":{"name":"...","ecosystem":"npm"},"version":"..."}' https://api.osv.dev/v1/query` |
| 4 | The static-analysis rules against real-world rigs and scripts | run `rigfile pull --plan-only` on ten rigs you trust and ten random public ones; note false positives and misses; add fixtures |
| 5 | `/admin` in a real browser: release, reject, approve, verify (CSRF token, redirects) | staging deployment, one admin login |

## 4. Incident response

Read `docs/incident-response.md`, fill the bracketed contacts, and **run the three table-top exercises in §6 there** on a staging registry. Fix what was unclear. Until then the exit criterion "runbook written" is met on paper only.

## 5. Legal and privacy additions

The registry now stores a stronger set of facts (first-seen date, verification kind and a private note, signer identities, similar-name matches). Add them to the privacy policy you still need (`docs/stage-5-owner-checks.md` §4), and decide whether the private verification note may be disclosed to the verified person on request.

## 6. External security review

Stage 5's review was already outstanding. Stage 6 adds attack surface: the signature upload path, the admin page, the third-party OSV input, and the trust presentation (a reviewer should try to make the page show "signed by the publisher" for an attacker). Add `internal/sigverify`, `internal/analyze`, `internal/pkgcheck` and the `held`/review paths in `store_trust.go` to the list in `docs/registry-security.md` §5.

## 7. Exit criteria (plan §12, Stage 6)

| Criterion | Evidence today | Open |
|---|---|---|
| Signed releases, verification in the CLI, signer recorded | rig signatures: verified at upload and on every pull, signer in `state.json`; Rigfile's own releases are signed with minisign (Stage 4), **not** with Sigstore | Sigstore signing of Rigfile's own releases is a release-workflow change (`cosign sign-blob` in `release.yml`); not done |
| Verified publishers, popular-rig policy, typosquat detection, reputation signals | built and tested | live tuning |
| Static analysis of hooks and scripts; package vulnerability and malware lookup | built; measured on a self-written corpus; OSV against a fake | real-world calibration (§3 #3, #4) |
| Report abuse, moderation queue, yank | built (Stage 5 reports plus the `held` queue and `/admin`) | who staffs it |
| External security review passed | self-review only | **the review** |
| Incident response runbook written | written, never rehearsed | table-top exercise |
