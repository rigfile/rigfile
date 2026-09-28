# Getting started

This walkthrough takes you from nothing to a published rig in about five minutes: install the CLI, capture the setup you already have, review and apply it, then share it.

Rigs share over **GitHub** — `publish` creates and pushes the repository for you. Rigfile also has an optional registry server (`cmd/rigfile-registry`) you or anyone can self-host, for search and a few things git alone can't do; both are shown below, GitHub first. `https://your-registry.example` below stands for wherever a registry is actually running.

A **rig** is one `rigfile.yaml` plus the files it points to (instructions, skills, commands, hook scripts). It describes how an AI coding tool is set up: what it is told, which MCP servers it runs, which hooks fire, and what it is allowed to do. See [Concepts](concepts.md) for the full picture.

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

- To capture another tool, add `--from codex`, `--from cursor`, `--from gemini-cli` (and so on; see [Supported tools](tools.md)).
- Open `rig/rigfile.yaml` and read it. Instructions and hooks are your own text and may contain things you would rather not share.

## 3. See the plan, then apply

```sh
rigfile plan rig      # shows every change, writes nothing
rigfile apply rig     # the same plan, then asks before writing
```

The plan lists each file, section, hook, MCP server and permission rule the rig would add, change or remove, for every AI tool it targets, plus anything a tool cannot take. `apply` backs up everything it touches first; `rigfile rollback` undoes the last run. Applying always includes [`rigfile/base-secure`](security.md), a safety layer that sits under every rig.

If the rig uses secrets, store their values once (you are prompted; values are never echoed or printed):

```sh
rigfile secrets set alpaca/api_key
```

## 4. Share it

```sh
rigfile publish rig
```

This pushes to GitHub — it is not a dry run. `publish` rewrites your home-directory paths, blocks the output if it finds a secret, lists personal information for you to review (`--ack-personal` once you have), and checks that every package is pinned to an exact version; then it creates and pushes a repository named after the rig (`rig/rigfile.yaml`'s `name:`) under your `gh`-authenticated login, running `gh auth login` for you first if you're not signed in yet. Private by default; add `--public` to let anyone pull it. Want a different owner (an org, say)? `rigfile publish rig --to-github acme`. Just want to see the report without publishing anything? `rigfile publish rig --dry-run`.

```sh
rigfile pull github.com/your-login/my-rig          # how anyone else gets it
```

**Optional:** publish to a Rigfile registry instead (or as well), for search and a page with trust facts. Sign in once (a device flow: confirm a short code in your browser), then publish — a rig is private until you add `--public`:

```sh
rigfile login --registry https://your-registry.example
rigfile publish rig --to-registry --registry https://your-registry.example
```

## 5. Pull someone else's rig

```sh
rigfile pull github.com/owner/repo                                 # from GitHub
rigfile pull owner/name --registry https://your-registry.example   # or from a registry
```

You see who published it, how old it is, whether it is signed, what static analysis found, and the full plan. Nothing runs until you approve. Read [Pulling a rig safely](pulling.md) before you pull from someone you do not know.

## Next steps

- [Writing a rig](writing-a-rig.md): a complete, annotated `rigfile.yaml`.
- [Secrets](secrets.md): how values are stored and injected.
- [CLI reference](cli.md): every command.
