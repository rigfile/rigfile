# Getting started

This walkthrough takes you from nothing to a published rig in about five minutes: install the CLI, capture the setup you already have, review and apply it, then share it on this registry.

A **rig** is one `rigfile.yaml` plus the files it points to (instructions, skills, commands, hook scripts). It describes how an AI coding tool is set up: what it is told, which MCP servers it runs, which hooks fire, and what it is allowed to do. See [Concepts](/docs/concepts) for the full picture.

## 1. Install the CLI

Packaged releases are coming. For now, build from source (you need [Go](https://go.dev/dl/) 1.27 or newer and `git`):

```sh
git clone https://github.com/rigfile/rigfile
cd rigfile
go build -o rigfile ./cmd/rigfile
./rigfile doctor
```

Move the `rigfile` binary somewhere on your `PATH` (for example `~/.local/bin` or `/usr/local/bin`) so you can run it from any directory. The rest of this guide assumes you did.

`rigfile doctor` is a health check: it tells you which secret store is in use (your OS keychain, or an encrypted-file fallback), which AI tools it detected, and whether anything drifted since your last apply.

## 2. Capture what you already have

```sh
rigfile init --name your-login/my-rig
```

`init` reads your current Claude Code setup (instructions, skills, agents, commands, MCP servers, hooks, permissions) and writes a rig into `./rig`. It is **read-only**: nothing on your machine changes. Secret **values** are never captured; any secret it finds becomes a `secret://` reference instead.

- Use your **GitHub login, in lower case**, as the owner part of the name. The registry only accepts rigs under your own name (or an [organisation](/docs/collaboration) you belong to).
- To capture another tool, add `--from codex`, `--from cursor`, `--from gemini-cli` (and so on; see [Supported tools](/docs/tools)).
- Open `rig/rigfile.yaml` and read it. Instructions and hooks are your own text and may contain things you would rather not share.

## 3. See the plan, then apply

```sh
rigfile plan rig      # shows every change, writes nothing
rigfile apply rig     # the same plan, then asks before writing
```

The plan lists each file, section, hook, MCP server and permission rule the rig would add, change or remove, for every AI tool it targets, plus anything a tool cannot take. `apply` backs up everything it touches first; `rigfile rollback` undoes the last run. Applying always includes [`rigfile/base-secure`](/docs/security), a safety layer that sits under every rig.

If the rig uses secrets, store their values once (you are prompted; values are never echoed or printed):

```sh
rigfile secrets set alpaca/api_key
```

## 4. Share it

Sign in to this registry once (a device flow: you confirm a short code in your browser):

```sh
rigfile login --registry %REGISTRY%
```

Then publish:

```sh
rigfile publish rig --to-registry --registry %REGISTRY%
```

Before anything leaves your machine, `publish` rewrites your home-directory paths, blocks the upload if it finds a secret, lists personal information for you to review (`--ack-personal` once you have), and checks that every package is pinned to an exact version. The registry scans it again before it is published.

A rig is **private** until you say otherwise: only you can see or pull it. Add `--public` (or use the button on the rig's page) to let anyone pull it.

Tip: set `RIGFILE_REGISTRY=%REGISTRY%` in your shell profile and you can drop `--registry` from every command.

## 5. Pull someone else's rig

```sh
rigfile pull owner/name --registry %REGISTRY%
```

You see who published it, how old it is, whether it is signed, what static analysis found, and the full plan. Nothing runs until you approve. Read [Pulling a rig safely](/docs/pulling) before you pull from someone you do not know.

## Next steps

- [Writing a rig](/docs/writing-a-rig): a complete, annotated `rigfile.yaml`.
- [Secrets](/docs/secrets): how values are stored and injected.
- [CLI reference](/docs/cli): every command.
