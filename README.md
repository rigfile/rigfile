# Rigfile

**Docker + GitHub for AI agent setups.** A *rig* bundles your instructions, skills, subagents, slash commands, MCP servers, hooks and permissions into one reviewable `rigfile.yaml`. Pull someone's rig with a single command, see exactly what it will change, and approve it — Rigfile applies it to Claude Code, Codex CLI, Gemini CLI, Cursor, Claude Desktop, GitHub Copilot in VS Code, Zed and Devin, each in that tool's own format.

[![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Platforms](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](docs/guide/tools.md)
[![CI](https://github.com/rigfile/rigfile/actions/workflows/ci.yml/badge.svg)](https://github.com/rigfile/rigfile/actions/workflows/ci.yml)

> **Status: early.** macOS, Linux and Windows are all verified end to end on real machines. No packaged releases yet — build from source (one command, below). See [`docs/STATUS.md`](docs/STATUS.md) for exactly what's built and what's left.

## Why Rigfile

- **You reconfigure the same AI tool setup on every new machine.** `rigfile init` captures what you already have — instructions, skills, MCP servers, hooks, permissions — into one file. `rigfile apply` puts it back anywhere in seconds.
- **You want to share your setup, but not your API keys.** Rigs hold `secret://` references, never values. `rigfile publish` scans for anything that looks like a real secret and refuses to publish if it finds one.
- **You don't want to run someone else's hooks and MCP servers blind.** `rigfile pull` shows every file, hook, server and permission a rig would add, *before* anything runs — plus who published it, how old it is, and what static analysis found.
- **You use more than one AI tool.** Write a rig once; adapters translate it into each tool's own format, and the plan says plainly what a given tool can't take.

## Quick start

Needs [Go](https://go.dev/dl/) 1.27+ and `git`. No install script yet — three commands:

```sh
git clone https://github.com/rigfile/rigfile && cd rigfile
go build -o rigfile ./cmd/rigfile
./rigfile doctor
```

`doctor` is a health check — right after building, before you've applied anything, it looks like this (the two warnings are expected and go away once you `apply` a rig, next):

```
✔ platform         darwin/arm64
✔ rigfile on PATH  /usr/local/bin/rigfile
⚠ base-secure      not applied on this machine yet: run `rigfile apply <rig-dir>`
✔ scanner          224 rules (gitleaks v8.30.1 + Rigfile additions)
⚠ applied rig      nothing applied yet: run `rigfile apply <rig-dir>`
✔ secret store     OS keychain (macos-keychain)

OK with 2 warning(s)
```

Then capture, review, and share your own setup:

```sh
./rigfile init --name you/my-rig     # capture your current Claude Code setup — read-only, changes nothing
./rigfile plan rig                   # see exactly what applying would change — writes nothing
./rigfile apply rig                  # apply it, with a backup made first
./rigfile publish rig                # scrub it, then create + push github.com/<you>/my-rig
```

Anyone else, anywhere:

```sh
rigfile pull github.com/you/my-rig   # reviewed before anything runs; nothing happens until you approve
```

Full walkthrough, five minutes, start to a published rig: **[Getting started](docs/guide/getting-started.md)**.

## Features at a glance

| | |
|---|---|
| **Review before anything runs** | `plan`/`apply`/`pull` all show the full diff first; nothing is written until you approve |
| **Secrets never travel** | `secret://` references only; values live in your OS keychain and reach only the process that needs them |
| **A safety floor, always** | [`rigfile/base-secure`](docs/guide/security.md) — credential deny rules, a command guard, secret redaction, git secret scanning — applies under every rig and can't be removed |
| **One rig, many tools** | Claude Code, Codex CLI, Gemini CLI, Cursor, Claude Desktop, GitHub Copilot (VS Code), Zed, Devin — see [supported tools](docs/guide/tools.md) for exactly what each one gets |
| **Pinned and scanned** | Published versions are immutable; packages must be pinned to an exact version; a static scanner and a gitleaks-based secret scan run before anything is shared |
| **Undo anything** | Every `apply` is backed up and recorded; `rigfile diff` shows drift, `rigfile rollback` restores |

## Documentation

**[docs/guide](docs/guide/README.md)** — plain Markdown, meant to be read here on GitHub:

- [Getting started](docs/guide/getting-started.md) · [Concepts](docs/guide/concepts.md) · [Writing a rig](docs/guide/writing-a-rig.md)
- [Secrets](docs/guide/secrets.md) · [Publishing & sharing](docs/guide/publishing.md) · [Pulling a rig safely](docs/guide/pulling.md)
- [Manifest reference](docs/guide/manifest.md) · [CLI reference](docs/guide/cli.md) · [Supported tools](docs/guide/tools.md)
- [Security model & limits](docs/guide/security.md) · [FAQ & troubleshooting](docs/guide/faq.md)

Design docs and specs (merge semantics, the registry, the secret broker, private sync, per-tool research) live in `docs/`; ADRs in `docs/adr/`. Master plan: [`RIGFILE_PLAN.md`](RIGFILE_PLAN.md); current build state: [`docs/STATUS.md`](docs/STATUS.md).

## Registry: an optional layer

Sharing through GitHub is the primary, recommended way — no new account, no new service to trust, just `gh` (which `rigfile logins` already uses) and the GitHub account you already have. Rigfile also ships a self-hostable registry (`cmd/rigfile-registry`) for what git alone doesn't give you: cross-rig search, `from:` layers pinned to a semver range instead of a fixed ref, and publisher trust facts. Real, tested, not required, and no instance is publicly hosted right now.

```sh
rigfile login --registry https://your-registry.example
rigfile publish rig --to-registry --registry https://your-registry.example   # private until you add --public
rigfile pull owner/name --registry https://your-registry.example
```

## Repository layout

| Path | What |
|---|---|
| `cmd/rigfile` | The CLI |
| `cmd/rigfile-registry`, `internal/registry` | The registry service and website (`Dockerfile.registry`, `fly.toml`, `deploy/`) |
| `internal/adapters/*`, `internal/targets` | One adapter per AI tool, and their capability files |
| `internal/{manifest,merge,layers,lock,apply,engine}` | Parsing, layering and applying a rig |
| `internal/{secrets,execshim,rigd,sandbox,scan,hook}` | Secrets, `rigfile exec`, the broker, confinement, the secret scanner, built-in hooks |
| `internal/platform` | Every OS difference lives here and only here |
| `base-secure/`, `schema/`, `catalog/` | The safety layer, the manifest JSON Schema, tool and model catalogs |
| `e2e/` | Container and VM end-to-end tests |

## Contributing

Issues and PRs are welcome. Before opening one:

```sh
gofmt -l .                  # must print nothing
go vet ./... && GOOS=windows go vet ./...
go test -race ./...
scripts/registry-test.sh    # registry tests against a throwaway Postgres (Docker)
e2e/run.sh                  # the CLI end to end in Linux containers
```

Tests never touch the real machine: they use a temporary `$HOME`, fakes for the keychain and package managers, and fake secrets only (a real credential anywhere in a commit is rejected by CI). Commits follow [Conventional Commits](https://www.conventionalcommits.org/). One platform rule worth knowing before you touch OS-specific code: every OS difference lives in `internal/platform` and only there — no hard-coded `/`, `~`, `C:\`, `brew` or `sh` anywhere else, so the same code path compiles and passes on macOS, Linux and Windows.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md). [Security model & limits](docs/guide/security.md) documents what Rigfile protects, how, and what it doesn't.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
