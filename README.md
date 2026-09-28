# Rigfile

**Share how you set up your AI coding tools.** A *rig* bundles instructions, skills, subagents, slash commands, MCP servers, hooks and permissions into one reviewable `rigfile.yaml`. Rigfile applies it to Claude Code, Codex CLI, Gemini CLI, Cursor, Claude Desktop, GitHub Copilot in VS Code, Zed and Devin, each in the tool's own format.

- **Nothing runs unreviewed.** `plan`, `apply` and `pull` show every file, hook, server and permission first, and change nothing until you approve.
- **Secrets never travel.** Rigs hold `secret://` references; values stay in your OS keychain and reach only the process that needs them.
- **A safety floor under every rig.** `rigfile/base-secure` (credential deny rules, a command guard, secret redaction, git secret scanning) is always applied and cannot be removed.
- **Undo anything.** Every apply is backed up; `rigfile rollback` restores it.

Documentation: **[docs/guide](docs/guide/README.md)**.

> Status: early. macOS and Linux are tested end to end on real machines; Windows is built but not yet verified on a real machine. No packaged releases yet: build from source.

## Build

Needs Go 1.27 or newer.

```sh
go build -o rigfile ./cmd/rigfile
./rigfile doctor
```

## A one-minute tour

Rigs share over plain git — any host, no account needed:

```sh
rigfile init --name you/my-rig          # capture your current Claude Code setup into ./rig (read-only)
rigfile plan rig                        # see exactly what applying would change; writes nothing
rigfile apply rig                       # apply it, with a backup first
rigfile secrets set alpaca/api_key      # store a secret the rig references (never printed)

rigfile publish rig --to-git ../my-rig-repo --git-init   # then push it anywhere: GitHub, GitLab, your own git host
rigfile pull github.com/you/my-rig-repo                  # someone else's rig, reviewed before anything runs
rigfile rollback                        # undo the last run
```

There's also an optional registry server (search, version-range layering with `from:`, trust facts) — see [Registry: an optional layer](#registry-an-optional-layer) below.

Every command: `rigfile --help`, or the [CLI reference](docs/guide/cli.md).

## Registry: an optional layer

`git` distribution is the primary, recommended way to share rigs: it needs no new account, no new service to trust, and works with hosts people already use. Rigfile also ships a registry server (`cmd/rigfile-registry`, `internal/registry`) for what git alone doesn't give you: cross-rig search, `from:` layers pinned to a semver range (`^1.2`) rather than a fixed ref, and publisher/trust facts (first-seen date, stars, verified badge). It's real, tested and self-hostable (`Dockerfile.registry`, `deploy/docker-compose.yml`) — not required, and no instance is publicly hosted right now.

```sh
rigfile login --registry https://your-registry.example
rigfile publish rig --to-registry --registry https://your-registry.example   # private until you add --public
rigfile pull owner/name --registry https://your-registry.example
```

## Documentation

- User docs (getting started, writing a rig, secrets, publishing, pulling safely, manifest and CLI reference, supported tools, security model, FAQ): [**docs/guide**](docs/guide/README.md), plain Markdown, meant to be read here on GitHub. This is the maintained copy. A self-hosted registry also serves a `/docs` website from `internal/registry/web/docs/` — a frozen snapshot from when docs/guide was created; it is not kept in sync and should not be edited.
- Design and specs: `docs/` (merge semantics, `base-secure`, trust and supply chain, the registry, the secret broker, private sync, per-tool research in `docs/targets/`) and the ADRs in `docs/adr/`.
- Master plan: `RIGFILE_PLAN.md`; current state: `docs/STATUS.md`.

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

## Development

```sh
gofmt -l .                  # must print nothing
go vet ./... && GOOS=windows go vet ./...
go test -race ./...
scripts/registry-test.sh    # registry tests against a throwaway Postgres (Docker)
e2e/run.sh                  # the CLI end to end in Linux containers
```

Tests never touch the real machine: they use a temporary `$HOME`, fakes for the keychain and package managers, and fake secrets only. Commits follow Conventional Commits. See `CLAUDE.md` for the full working rules.

## Security

Please report vulnerabilities privately; see [SECURITY.md](SECURITY.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
