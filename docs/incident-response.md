# Incident response runbook (S6-M6)

Status: **draft written by the developer, never rehearsed.** It is only as good as the first table-top exercise; schedule one (`docs/stage-6-owner-checks.md` §4). Placeholders in [brackets] are yours to fill.

## 0. Principles

1. **Stop the harm first, understand it second.** Every tool below is reversible except deleting data, and nothing here deletes data.
2. **Rotate before you clean.** A leaked credential stays leaked until it is rotated, whatever you do to the copy.
3. **Write everything down as you go** (time, who, what, why) in one place. The registry's audit log records the tool actions; it does not record your reasoning.
4. **Tell people early and plainly** what happened, what they should do, and what you do not yet know.

## 1. Roles and contacts

| Role | Who | Reach them by |
|---|---|---|
| Incident lead (decides; runs the tools) | [name] | [phone / chat] |
| Second pair of hands (checks each destructive step) | [name] | [phone / chat] |
| Communications (status page, user notices) | [name] | [phone / chat] |
| Hosting/database access | [name] | [where the credentials live] |

Severity: **S1** users can be harmed now (malicious rig being pulled, credential leak, compromised signing key or registry host). **S2** harm is possible but not yet happening. **S3** everything else. S1 gets the tools in §2 within 15 minutes.

## 2. The first fifteen minutes (S1)

| Step | Command (on the registry host, with the service's environment) | Effect |
|---|---|---|
| Stop new uploads | `rigfile-registry admin publishing pause --reason "investigating"` | uploads answer 503 with the reason; reads continue |
| Pull a bad version out of circulation | `rigfile-registry admin takedown --rig owner/name --version X.Y.Z --reason "..."` (omit `--version` for the whole rig) | unavailable to everyone but admins; the blob is kept as evidence |
| Stop a publisher | `rigfile-registry admin disable-user --login L --reason "..."` | their sessions and tokens stop working; new sign-ins are refused |
| Cut every CLI session | `rigfile-registry admin revoke-tokens --all` (or `--login L`) | all API tokens revoked; people run `rigfile login` again |
| See what happened | `rigfile-registry admin audit --limit 200` and `admin reports` | who did what, when |
| Freeze evidence | copy the database and bucket listing to a location the incident lead controls before changing more | nothing is deleted by the tools above, but backups made later are the record of *now* |

Web sessions are ended by disabling the user or rotating the database (there is no "log out everyone" button; a session is refused as soon as its user is disabled).

## 3. Playbooks

### 3.1 A malicious rig is in circulation

1. Pause publishing (§2). Take the version down; if the publisher may be involved, disable the account.
2. Find the blast radius: `admin audit` for the upload; the rig's version list; `GET /v1/rigs/<owner>/<name>` for stars as a proxy for reach. The registry does not count downloads: assume any pull since the upload is affected.
3. Publish a notice: what the rig did (from the static-analysis findings and your reading of it), which versions, how to tell whether a machine pulled it (`rigfile diff`/`state.json` shows `source` and `commit`), and what to do (`rigfile rollback`, rotate any secret the rig could reach, review `~/.claude` and shell rc files, remove MCP servers it added).
4. Check other rigs by the same publisher and rigs with similar names; take down what is related.
5. Resume publishing once the cause is understood. Write down which scan rule would have caught it and add a fixture (`internal/analyze`).

### 3.2 A publisher account is taken over

Signs: a new version nobody expected, a changed signer on `rigfile update` (users are refused without `--accept-signer-change`), the publisher tells you.

1. Disable the account; take down versions published since the takeover; revoke its tokens.
2. Ask the real owner to secure the GitHub account (revoke sessions and tokens, check authorised apps and SSH keys, enable 2FA), and to rotate anything the compromised workflows could reach.
3. Re-enable the account only after the owner confirms. Their old versions stay as they were; ask them to publish a new signed version.
4. Notice to users of that publisher's rigs (as 3.1 step 3).

### 3.3 A registry credential leaked (database URL, bucket keys, GitHub OAuth secret, host access)

1. Rotate the credential at its source first: database password, bucket access keys, the GitHub OAuth app's client secret (Settings > Developer settings; this invalidates in-flight sign-ins only), host keys.
2. Update the service's environment and restart. Confirm `GET /healthz`.
3. Assume the database content is readable: it holds account names, GitHub ids, hashed tokens and session ids (hashes are not usable as credentials), audit history and rig content. Revoke all tokens as a precaution (`admin revoke-tokens --all`).
4. If the *bucket* keys leaked, rig tarballs could have been replaced. Tarballs are content-addressed and the CLI checks the hash it pinned, so a swapped blob fails on pull; verify by re-hashing every blob against `versions.tarball_sha256`.
5. Review the audit log and host logs for use of the credential.

### 3.4 The registry host is compromised

1. Take it out of service (block traffic at the proxy or DNS); do not "clean" it in place.
2. Rebuild from the image on a fresh host with rotated credentials (3.3), restore the database and bucket from a backup taken *before* the compromise, and compare.
3. Because the CLI verifies tarball hashes and (for signed rigs) Sigstore signatures on the user's machine, a compromised registry cannot silently alter a rig a user already pinned; it can serve new, unsigned content to first-time pullers. Tell users to use `--require-signature` for rigs where it matters and to review plans of anything pulled during the window.

### 3.5 A release-signing key leaked or was misused

*The minisign key that signs Rigfile releases* (`docs/stage-4-owner-checks.md` §3):

1. Stop the release workflow (disable it in GitHub; remove the secrets).
2. Generate a new key. Every installed binary trusts the **old** public key (it is compiled in), so a new key can only reach users through a release those users install by another route (package manager, a new `install.sh` fetched over HTTPS from the repository, not through `self-update`, which will verify against the old key).
3. Publish an advisory with the new public key's fingerprint through several channels, revoke the old key's use (delete the old release assets that carried it if you cannot trust them), and ship a new release signed with the new key.
4. Review every release signed since the earliest possible compromise.

*A publisher's Sigstore identity* is their GitHub Actions workflow identity: a compromised workflow or repository is 3.2 plus rotating the repository's secrets and pinning workflow dependencies by hash.

### 3.6 A vulnerability is reported in Rigfile or the registry

1. Acknowledge within [2 business days]. Reproduce. Decide severity.
2. Fix on a private branch; release as a normal signed release; for the registry, deploy.
3. Credit the reporter if they wish. Publish an advisory that says what was affected and how to check.

### 3.7 The scan itself is being evaded

A rig that passes the scan and is harmful is 3.1 plus a new fixture. If evasion is systematic, tighten the rule and re-scan: there is no tool to re-scan stored versions yet (a follow-up: `admin rescan`); until then, hold new uploads (§2) and review by hand.

## 4. Communications

Status notice template:

> **[date, time UTC] Registry incident: [short title].** What happened: [facts only]. Who is affected: [pulled or published X between A and B]. What we have done: [paused publishing / removed version / rotated credentials]. What you should do: [`rigfile rollback`, rotate ..., re-run `rigfile login`]. What we do not know yet: [list]. Next update by [time].

Post it where users will look (repository, registry home page banner (not built yet: edit the template), social accounts) and email affected publishers directly.

## 5. After

Within a week: a written timeline, the root cause, what detection would have caught it earlier, and the changes made (a rule, a test, a limit, a procedure). Update this runbook with what you learned; add the failure to `docs/registry-security.md`.

## 6. Table-top exercise (do this before launch)

Run each of these with the people in §1, reading the runbook aloud and timing each step on a staging registry:

1. A public rig with 200 stars publishes a new version that reads `~/.aws` and posts to a webhook. The scan holds it; a reporter says a machine was already affected by the previous version. What do you do, in what order?
2. Your GitHub OAuth client secret appears in a public paste.
3. A user reports that `rigfile self-update` offered a binary whose signature does not verify.

Record what was unclear, missing or slow, and fix the runbook.
