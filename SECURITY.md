# Security policy

## Reporting a vulnerability

Please report vulnerabilities in Rigfile (the CLI, base-secure, the registry service, the release and installer tooling) **privately**, not in a public issue.

- Email: [SECURITY CONTACT EMAIL: to be set by the owner before the first public release]
- Or use GitHub's private vulnerability reporting on the repository, if enabled: [REPOSITORY]/security/advisories/new

Include what you found, how to reproduce it, and what you think the impact is. Please do not access other people's data, degrade the service, or test against accounts you do not own.

We aim to acknowledge a report within [2 business days], tell you our assessment within [7 days], and fix serious issues promptly. We will credit you in the advisory if you want that. [LAWYER: safe-harbour statement for good-faith research.]

## What is in scope

- The `rigfile` CLI and its handling of secrets, files, git hooks and signatures.
- `rigfile/base-secure` and the per-tool configuration it writes.
- The registry service (`rigfile-registry`), its API and web pages.
- Release artefacts, `install.sh`, `install.ps1`, `rigfile self-update`.

## Not in scope

- Malicious rigs published by third parties: report those through the registry's "Report this rig" link or, if you cannot, at the address above. They are handled under the takedown policy, not as vulnerabilities in Rigfile.
- Findings that need a compromised machine, a malicious local user with your privileges, or social engineering of the maintainers.
- Denial of service by volume.

## Supported versions

Only the latest release receives security fixes.

See also: `docs/registry-security.md` (what the registry protects and what it does not), `docs/trust.md` (what the trust signals mean), and `docs/incident-response.md`.
