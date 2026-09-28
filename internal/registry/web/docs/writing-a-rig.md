# Writing a rig

The fastest start is `rigfile init --name you/my-rig`, which captures what you already have. This page walks through a complete `rigfile.yaml` written by hand, section by section. Every field is also listed in the [Manifest reference](/docs/manifest).

## A complete example

```text
data-science/
├── rigfile.yaml
├── instructions/coding-style.md
├── skills/job-pipeline/SKILL.md
├── agents/code-reviewer.md
├── commands/ship.md
└── hooks/notify.sh
```

```yaml
apiVersion: rigfile.dev/v1
name: you/data-science          # owner/name, lower case; owner = your GitHub login or an organisation
version: 1.2.0                  # semantic version; a published version can never change
description: Python/data-science rig with strict git hygiene
license: MIT

from:                           # layers underneath this rig (base-secure is always first, implied)
  - acme/python-dev@^2

targets:
  include: [claude-code, codex, cursor, gemini-cli]

instructions:
  - id: coding-style
    file: instructions/coding-style.md
    scope: user                 # user (your home config) or project (a repository)

skills:
  - path: skills/job-pipeline   # a folder with a SKILL.md
  - ref: skills.sh/anthropics/pdf@1.0.3   # an external skill, pinned

agents:
  - path: agents/code-reviewer.md
    targets: [claude-code]      # only where it makes sense

commands:
  - path: commands/ship.md      # becomes /ship

mcp_servers:
  alpaca:
    command: npx
    args: ["-y", "@example/alpaca-mcp-server@1.4.2"]   # pinned to an exact version
    env:
      ALPACA_API_KEY: secret://alpaca/api_key         # a reference, never a value
      ALPACA_SECRET_KEY: secret://alpaca/secret_key
      ALPACA_PAPER: "true"                            # plain values are fine for non-secrets
    network:
      allow: ["paper-api.alpaca.markets", "data.alpaca.markets"]
  docs:
    transport: http
    url: https://mcp.example.com/mcp                  # remote servers must use HTTPS
    auth: oauth

hooks:
  - id: notify-on-finish
    event: stop
    run: { macos: hooks/notify.sh, linux: hooks/notify.sh, windows: hooks/notify.ps1 }

permissions:
  deny:
    - read: "**/secrets/**"
  ask:
    - bash: "git push*"

tools:
  common: [gh, uv, jq]
  pipx: [ruff]

secrets:
  alpaca/api_key:
    description: Alpaca API key (paper)
    obtain_url: https://app.alpaca.markets/paper/dashboard/overview
    hosts: ["*.alpaca.markets"]
  alpaca/secret_key:
    description: Alpaca secret key (paper)
    hosts: ["*.alpaca.markets"]

logins:
  - provider: github
    reason: GitHub MCP server
```

Check it at any time:

```sh
rigfile validate data-science
rigfile plan data-science
```

## Section by section

### Identity

`apiVersion`, `name` and `version` are required. `name` is `owner/name` in lower case (letters, digits, hyphens; the name part may also use `.` and `_`). To publish on the registry, the owner must be your GitHub login or an organisation you belong to.

### `instructions`

Markdown written into each tool's instruction file (`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, Cursor rules) as a **marked section**. Your own text in those files is never replaced. `scope: project` writes into a repository (pass `--project <dir>` to `plan`/`apply`); `user` writes into your home configuration.

### `skills`, `agents`, `commands`

- **Skills** are folders with a `SKILL.md` (the Agent Skills format), or an external `ref` that must be pinned (`@1.0.3`) to publish publicly.
- **Agents** are Markdown files with frontmatter (subagents). Adapters convert them where the tool has an equivalent (Codex uses TOML) and say so where it does not.
- **Commands** are reusable prompts, invoked as `/name`. Codex has deprecated custom prompts, so the Codex adapter turns commands into skills.

### `mcp_servers`

Keyed by name. Two shapes:

- **stdio** (`command` + `args`): a program the tool starts. The package must be **pinned to an exact version** inside `args`, in the form the launcher understands:

  | Launcher | Pinned form |
  |---|---|
  | `npx`, `bunx`, `pnpx` | `package@1.2.3` or `@scope/package@1.2.3` |
  | `uvx` | `package==1.2.3` (or `package@1.2.3`) |
  | `pipx run` | `package==1.2.3` |

  Unpinned packages are allowed locally (with a warning) but block `rigfile publish` (to the registry or to git). Rigfile starts stdio servers through `rigfile exec`, which injects the server's secrets into its environment only.
- **remote** (`transport: http` + `url`): must be `https://`, with no credentials in the URL. `auth: oauth` walks you through the login; `bearer` takes a `bearer_token: secret://...`.

`network.allow` lists the hosts the server needs; with the [secret broker](/docs/secrets) turned on, it is enforced.

### `hooks`

Commands that run on agent events: `pre_tool_use`, `post_tool_use`, `permission_request`, `user_prompt_submit`, `session_start`, `session_end`, `stop`, `subagent_start`, `subagent_stop`, `pre_compact`, `post_compact`, `notification`. `run` is a script inside the rig (optionally different per OS, as above) or a built-in (`builtin:guard`, `builtin:write-guard`, `builtin:redact`, the same binary code on every OS). `match` narrows a hook to a tool or command; `timeout_seconds` bounds it.

Hooks execute code on the machine that applies the rig. The plan marks every one with "executes code", and pulling someone else's rig shows each script before you approve.

### `permissions`

Three lists, `deny`, `ask` and `allow`, of rules of one kind each: `read`, `edit`, `bash`, `powershell`, `web_fetch` (a domain) or `mcp` (a server, or `server:tool`). Add `reason` to explain a rule. Deny always wins across layers, and no layer can remove a deny from a layer beneath it (including base-secure's).

### `tools`

Command-line tools the rig expects: `common` names from Rigfile's catalog (resolved to the right package for each OS and package manager), plus per-manager lists (`npm`, `pipx`, `uv`, `cargo`, `go`) and per-OS escape hatches (`macos: { brew: [...] }`, `linux: { apt: [...], dnf: [...] }`, `windows: { winget: [...], scoop: [...] }`). The plan lists each install; `--no-tools` shows them without installing.

### `secrets`

Declarations only: a description, where to get the value (`obtain_url`), the hosts it may be sent to (`hosts`), and whether it is `optional`. Values are stored per machine with `rigfile secrets set`. See [Secrets](/docs/secrets).

### `logins`

Sign-ins the rig needs (`provider`, `reason`, and a `method`: `oauth`, `vendor-cli` or `api-key`). After applying, `rigfile logins` walks you through them.

### Per-OS and per-tool items

Almost every item accepts `os: [macos, linux, windows]` and `targets: [...]` to limit where it applies. `overrides.<target>` replaces or adds items for one tool only.

### `private`

Paths that are never published and only [synced between your own machines](/docs/sync).

## Paths and portability

Write paths with forward slashes, relative to the rig. Never hard-code `/Users/you` or `C:\Users\you`: the validator rejects home paths, and `publish` rewrites any it finds. Rigfile maps each tool's configuration directory per OS for you.
