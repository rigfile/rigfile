# FAQ and troubleshooting

## Questions

### Is a rig safe to pull?

Only as safe as its author. Rigfile makes a rig's behaviour visible (the plan, every hook and MCP command, trust facts, signature, static analysis) and applies nothing until you approve, with base-secure underneath and a rollback for every run. Read the plan. Prefer signed rigs from publishers you recognise. See [Pulling a rig safely](/docs/pulling).

### Will Rigfile overwrite my existing configuration?

No. Instructions go into marked sections of your files, settings are edited structurally, and content you edited by hand is reported as a conflict and left alone unless you pass `--overwrite`. Everything is backed up first; `rigfile rollback` restores it.

### Where are my secrets?

In your operating system's keychain (or an encrypted file where there is none). Never in a rig, never in the tool's configuration files. See [Secrets](/docs/secrets).

### Does it work on Windows?

macOS and Linux are tested end to end on real machines. Windows support is built (each adapter maps the tool's Windows paths, and the CLI compiles for Windows) but has **not yet been verified on a real Windows machine**; treat it as a preview. Some protections are weaker on native Windows either way (Claude Code has no sandbox there).

### Can I keep a rig private?

Yes. Rigs on this registry are private until you make them public; to everyone else a private rig does not appear to exist. You can also keep rigs in a private git repository and pull from it.

### Can a team share rigs?

Yes: create an [organisation](/docs/collaboration) and publish rigs as `org/name`. Every member can publish.

### What does "UNVERIFIED" mean in the docs and on the plan screen?

That the vendor's documentation did not settle a detail when it was checked. Rigfile does not guess: it skips the item or says it is unconfirmed, rather than writing something that might be wrong.

## Errors people actually hit

### `no registry is configured: pass --registry ... or set RIGFILE_REGISTRY`

Commands that talk to a registry need its address. Add `--registry %REGISTRY%`, or put this in your shell profile:

```sh
export RIGFILE_REGISTRY=%REGISTRY%
```

### `package "x" is not pinned to an exact version (want name==1.2.3)` and `not published: ... a public rig must pin what it runs`

`publish` requires every MCP server package to be pinned, for private rigs too (a private rig can be made public later without a new upload). `1.2.3` is only the format; use the package's real current version:

```yaml
mcp_servers:
  alpaca:
    command: uvx
    args: ["alpaca-mcp-server==2.3.2"]   # uvx and pipx: name==version
```

For `npx`, `bunx` and `pnpx` the form is `package@1.2.3` (or `@scope/package@1.2.3`).

### `you can publish only under your own name (you/...) or an organisation you belong to`

The rig's `name:` must start with your registry login (your GitHub login in lower case) or an organisation you are a member of. `rigfile init` names new rigs `local/my-rig` unless you pass `--name`; change the `name:` line in `rigfile.yaml`.

### `... already exists; versions are immutable, publish a new version number`

Bump `version:` in `rigfile.yaml` (for example `1.0.0` to `1.0.1`) and publish again.

### A rig page says "Not found" but I know it exists

It is private and you are not signed in as its owner (or a member of its organisation). Sign in with the account that published it.

### `no published version satisfies that`

The rig has no published version you can see: it is private, still being scanned (pending), was rejected, or the version range matches nothing. Check the rig page while signed in as the owner.

### `warning: no OS keychain available; using the encrypted-file backend`

You are on a machine without a usable keychain (a server, a container, a Linux box without a Secret Service). Values are stored in an encrypted file instead; `rigfile doctor` flags it as weaker. For scripts, set `RIGFILE_PASSPHRASE_FILE` to a file only you can read.

### The plan says a tool is `NOT CONFIGURED: not detected`

Rigfile only configures tools it finds on the machine (Claude Code is always configured). Install the tool, or pass `--target <tool>` to configure it anyway.

### The plan shows a conflict on a file I edited

You changed content Rigfile manages. Keep your version (do nothing), or pass `--overwrite` to replace it with the rig's.

## Still stuck?

Run `rigfile doctor`, and open an issue on [GitHub](https://github.com/rigfile/rigfile/issues) with its output (it never prints secret values).
