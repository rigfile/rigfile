# Publishing and sharing

You can share a rig two ways: write it as a **git repository** you host anywhere (the primary, recommended way — no account needed), or publish it to **a Rigfile registry** (optional: search and version-range layering, and needs one to be running). Both go through the same safety pipeline first.

## What `publish` does before anything leaves your machine

1. **Scrub.** Absolute paths under your home directory are rewritten to portable ones. Personal information it notices (e-mail addresses, phone numbers, your user and host name) is listed for you to review.
2. **Secret scan.** Any finding blocks the publish. There is no override: remove the value and use a `secret://` reference.
3. **Validate.** The manifest must be valid, and every package an MCP server runs must be **pinned to an exact version** (see [Writing a rig](writing-a-rig.md)).
4. **Stage and re-scan.** The files are written to a temporary directory and scanned again from disk; only a clean result is published.
5. It adds a generated `README.md` describing what is inside and a `.gitignore`.

If personal information was found, re-run with `--ack-personal` once you have checked the list.

Run `rigfile publish my-rig` with no destination to do all of this as a **dry run**: it reports what it found and publishes nothing.

## To git (recommended)

```sh
rigfile publish my-rig --to-git ../my-rig-repo --git-init
```

This writes the scrubbed rig into an empty directory (and, with `--git-init`, makes one commit). Push it to any host — public or private, GitHub, GitLab, your own. Others pull it with:

```sh
rigfile pull github.com/you/my-rig-repo
rigfile pull github.com/you/my-rig-repo@v1.2.0
```

## To the registry (optional)

Useful when you want your rig searchable, or you're using `from:` with a version **range** (`@^1.2`) rather than a fixed git ref — that resolution is registry-only (see [Concepts](concepts.md)).

```sh
rigfile login --registry https://your-registry.example
rigfile publish my-rig --to-registry --registry https://your-registry.example
```

- The rig's `name` must be `your-login/name` (your GitHub login, lower case) or `org/name` for an [organisation](collaboration.md) you belong to.
- The registry scans the upload again. Until the scan finishes, the version is *pending* and visible only to you; a rejected version shows you why, and its archive is deleted.
- **Versions are immutable.** To change a rig, bump `version:` and publish again. The previous versions stay available, and every rig page links a readable diff between versions.
- New rigs are **private**: only you (or your organisation's members) can see and pull them. Make one public with `--public` on publish, or the **Make public** button on the rig's page.
- Publishing under a reserved name (`rigfile/`, and names of AI vendors) is not allowed.

### Signing (registry only, optional)

A rig published to the registry can carry a Sigstore signature over its archive:

```sh
rigfile publish my-rig --write-tarball rig.tgz          # dry run that writes the exact archive
cosign sign-blob --bundle rig.sigstore.json rig.tgz
rigfile publish my-rig --to-registry --sign-bundle rig.sigstore.json
```

The registry and every puller verify it, and the rig page says whether it was signed by the publisher's own identity (a GitHub Actions workflow in the publisher's account, or their GitHub sign-in). Pullers can insist on it with `--require-signature`. There is no signing path for a git-only publish yet.

### Yanking

A published version can be *yanked* by its publisher (or an organisation member) with a reason, through the registry API (`POST /v1/rigs/{owner}/{name}/versions/{version}/yank`). A yanked version stays pullable by its exact number, with a visible warning, but version ranges (`@^1.2`) skip it.

## Publishing a capture directly

Without a rig directory, `publish` captures your current setup and publishes it in one step. It shows a checklist of what it found so you can leave things out (`--all` takes everything):

```sh
rigfile publish --name you/my-setup --to-git ../my-setup-repo --git-init
```

Use `--from codex` (or another tool) to capture something other than Claude Code, and `--to-registry --registry https://your-registry.example` instead if you'd rather publish there.
