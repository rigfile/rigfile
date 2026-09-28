# Manifest reference

Every field of `rigfile.yaml` (`apiVersion: rigfile.dev/v1`). The authoritative definition is the JSON Schema shipped with the CLI (`schema/rigfile.v1.json`); `rigfile validate <dir>` checks a rig against it and against the rules a schema cannot express. For a guided tour, see [Writing a rig](writing-a-rig.md).

Items in most lists accept `os: [macos, linux, windows]` and `targets: [<tool>, ...]` to limit where they apply.

## Top level

| Field | Required | Type | Meaning |
|---|---|---|---|
| `apiVersion` | yes | `rigfile.dev/v1` | Manifest format version. |
| `name` | yes | `owner/name` | Lower case. The owner must be your registry login or an organisation you belong to. |
| `version` | yes | semver | `1.2.0`, `2.0.0-rc.1`. Published versions are immutable. |
| `description` | | text | One line, shown on the registry. |
| `license` | | SPDX id | `MIT`, `Apache-2.0`, ... |
| `from` | | list of `owner/name@range` | Layers underneath this rig, in order. `rigfile/base-secure` is always first and implied. |
| `targets` | | `{include: [...], exclude: [...]}` | Which tools the rig is meant for. Tools outside it get a warning, not an error. |
| `instructions` | | list | Markdown sections for the tools' instruction files. |
| `skills` | | list | Agent Skills folders or pinned external skills. |
| `agents` | | list | Subagent definitions. |
| `commands` | | list | Slash commands / reusable prompts. |
| `mcp_servers` | | map | MCP servers keyed by name. |
| `hooks` | | list | Commands run on agent events. |
| `permissions` | | `{deny, ask, allow}` | Permission rules. |
| `tools` | | object | Command-line tools to install. |
| `secrets` | | map | Declarations of the secrets the rig uses (never values). |
| `logins` | | list | Sign-ins the rig needs. |
| `models` | | map | Local models. |
| `gateways`, `routing` | | object | Local protocol bridges and model routing. Parsed and merged, **not yet applied**. |
| `overrides` | | map of tool → block | Per-tool replacements or additions. |
| `private` | | list of paths | Never published; only synced between your own machines. |

## `instructions[]`

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Identifier, unique in the rig. |
| `file` | yes | Markdown file inside the rig. |
| `scope` | | `user` (default: your home configuration) or `project` (a repository; pass `--project <dir>`). |
| `merge` | | `section` (the only strategy): inserted as a marked section, never replacing your text. |

## `skills[]`

Either `path:` (a folder inside the rig containing `SKILL.md`) or `ref:` (an external skill such as `skills.sh/anthropics/pdf@1.0.3`, which must be pinned to publish publicly). Optional `id`.

## `agents[]`, `commands[]`

| Field | Required | Meaning |
|---|---|---|
| `path` | yes | A Markdown file inside the rig. |
| `id` | | Identifier, unique in the rig. |

## `mcp_servers.<name>`

**stdio server** (`command` required):

| Field | Meaning |
|---|---|
| `transport` | `stdio` (default). |
| `command` | The program, e.g. `npx`, `uvx`, `node`. |
| `args` | Arguments. The package **must be pinned** here: `pkg@1.2.3` (npx/bunx/pnpx), `pkg==1.2.3` (uvx, pipx run). |
| `env` | Environment variables: plain values, or `secret://...` references for secrets. |
| `cwd` | Working directory. |
| `network.allow` | Hosts the server may reach (enforced with the broker). |

**Remote server** (`transport: http` and `url` required):

| Field | Meaning |
|---|---|
| `url` | `https://` only, no credentials in it. |
| `auth` | `none` (default), `oauth` (guided login), or `bearer` (needs `bearer_token`). |
| `bearer_token` | A `secret://` reference. |
| `headers` | Static headers; credential-like values must be references. |

## `hooks[]`

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Identifier. |
| `event` | yes | `pre_tool_use`, `post_tool_use`, `permission_request`, `user_prompt_submit`, `session_start`, `session_end`, `stop`, `subagent_start`, `subagent_stop`, `pre_compact`, `post_compact`, `notification`. |
| `run` | yes | A script inside the rig, a map per OS (`{macos: ..., linux: ..., windows: ...}`), or a built-in: `builtin:guard`, `builtin:write-guard`, `builtin:redact`. |
| `match` | | Narrows the hook (for example `{tool: bash, command: "git commit*"}`). |
| `timeout_seconds` | | Upper bound on the hook's run time. |

## `permissions`

`deny`, `ask` and `allow` are lists of rules. Each rule has exactly one of:

| Kind | Example | Matches |
|---|---|---|
| `read` | `"~/.ssh/**"` | Reading files by path glob. |
| `edit` | `"**/.env"` | Writing or editing files. |
| `bash` | `"git push*"` | Shell commands by prefix or glob. |
| `powershell` | `"Get-ChildItem env:*"` | PowerShell commands (Windows). |
| `web_fetch` | `"example.com"` | Fetching from a domain. |
| `mcp` | `"github"` or `"github:create_issue"` | An MCP server or one of its tools. |

Optional `reason`, `os`, `targets`. A deny always wins, and no layer can remove a deny from a layer beneath it.

## `tools`

| Field | Meaning |
|---|---|
| `common` | Names from Rigfile's tool catalog (`gh`, `uv`, `jq`, `git`, `node`, `gitleaks`, ...), mapped to the right package per OS. |
| `npm`, `pipx`, `uv`, `cargo`, `go` | Packages for each language package manager. |
| `macos`, `linux`, `windows` | Per-OS escape hatches keyed by package manager: `brew`; `apt`, `dnf`, `pacman`; `winget`, `scoop`. |

## `secrets.<path>`

Keyed by the reference path (`alpaca/api_key` for `secret://alpaca/api_key`).

| Field | Required | Meaning |
|---|---|---|
| `description` | yes | What it is. |
| `obtain_url` | | Where to create or rotate it. |
| `hosts` | | The only hosts it may be sent to (enforced by the broker). |
| `optional` | | `true` if the rig works without it. |

## `logins[]`

| Field | Required | Meaning |
|---|---|---|
| `provider` | yes | For example `github`, `claude-code`. |
| `reason` | | Why the rig needs it. |
| `method` | | `oauth`, `vendor-cli` (runs the vendor's own login command) or `api-key`. |

## `models.<name>`

| Field | Meaning |
|---|---|
| `role` | A role from Rigfile's model catalog, resolved per machine (e.g. `local-coder`). |
| `purpose` | What the rig uses it for. |
| `serve` | `{host, port, api, autostart}`; the host must be loopback. |
| `variants` | Candidates; the first whose `when` matches this machine wins. |
| `license_ack` | The model's licence, shown on the plan screen. |

A variant has `when` (`os`, `arch`, `gpu`, `min_memory_gb`, `min_vram_gb`, `min_free_disk_gb`), `engine` (`ollama` or `mlx-lm` are applied), `engine_version`, `model` (a Hugging Face `org/name` or an Ollama `name:tag`), `revision` (40-hex commit, required for Hugging Face), `digest` (Ollama), `weights_format` (`safetensors`, `gguf`, `mlx-safetensors`) and `args` (engine flags). See [Local models](local-models.md).

## `overrides.<tool>`

A block with any of `instructions`, `skills`, `agents`, `commands`, `mcp_servers`, `hooks`, `permissions`, applied to one tool only. An override cannot weaken base-secure or remove a deny.
