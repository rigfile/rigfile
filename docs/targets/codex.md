# Target: Codex (CLI)

**Date checked:** 2026-09-25 (all sources below were fetched on this date)
**Method:** Official docs, read as raw markdown (append `.md` to a page URL). The docs now live under `https://learn.chatgpt.com/docs/…`; the older `developers.openai.com/codex/…` URLs still serve pages and their `llms.txt` index points at the new host. Only public documentation was used. Nothing was read from any real `~/.codex` directory.
**Legend:** **CONFLICT** = docs disagree with `RIGFILE_PLAN.md`. **UNVERIFIED** = docs silent or internally inconsistent; needs a real test before an adapter relies on it. **NOTE** = a fact that changes adapter design.
**Stability warning:** several Codex features that Rigfile needs are labelled **Beta** or **experimental** in the docs themselves (permission profiles, rules, subagent file format). Adapters must feature-detect and expect churn.

All snippets use obviously fake values.

---

## 0. Summary of plan corrections (Codex)

| # | Plan says | Docs say | Status |
|---|---|---|---|
| 1 | §9.2: skills "verify support/path" | Supported. User skills live in **`$HOME/.agents/skills`**, not under `~/.codex`. Repo skills in `.agents/skills`; admin in `/etc/codex/skills`. Format is the open Agent Skills standard (`SKILL.md`). | **NOTE**: Claude Code and Codex use *different* directories, so a portable skill is written twice (or symlinked). |
| 2 | §9.2: subagents "verify" | Supported, as **TOML** files: `~/.codex/agents/<name>.toml` and `.codex/agents/`. Required keys `name`, `description`, `developer_instructions`. | **NOTE**: not the Markdown+frontmatter format Claude Code uses; the adapter must convert. Docs say "the format may evolve". |
| 3 | §9.2: commands "verify prompts dir" | `~/.codex/prompts/*.md` exists but custom prompts are **deprecated**; docs say use skills. | **NOTE**: map Rigfile `commands:` to skills for Codex. |
| 4 | §9.2: hooks "verify" | Supported: `~/.codex/hooks.json` or inline `[hooks]` in `config.toml`. **Non-managed hooks must be reviewed and trusted by the user, recorded against the hook's hash**; new/changed hooks are skipped until trusted (`/hooks`). | **NOTE**: base-secure hooks cannot be silently activated on Codex; the plan's "one review screen" is not sufficient. Adds a manual step (or requires admin-managed `requirements.toml`). |
| 5 | §8.2: "approval on untrusted commands (verify option names)" | `approval_policy` accepts `on-request`, `never`, or a `granular` table. **`untrusted` is unsupported**, `on-failure` deprecated. | **CONFLICT** (plan wording outdated). |
| 6 | §8.2: canonical `permissions: deny/ask` | Two **mutually exclusive** systems: legacy `sandbox_mode` + `approval_policy`, or **beta** permission profiles (`default_permissions`, `[permissions.<name>]`). Command allow/prompt/forbid lives in a third place: `rules/*.rules`. | **NOTE**: one canonical `permissions` block fans out to up to three native mechanisms. See §8. |
| 7 | §9.5: Codex "native: `--oss` or a `model_providers` entry pointing at `http://127.0.0.1:8080/v1`" | `--oss` supports only the built-in `ollama` and `lmstudio` providers. Custom `model_providers.<id>.wire_api` accepts **`responses` only** (default). `mlx_lm.server` exposes only `/v1/chat/completions`, `/v1/completions`, `/v1/models` (checked in source, see below). | **CONFLICT**; see §9. |
| 8 | §8.2: keep secrets out of agent-readable files | Codex may cache login in **plaintext `~/.codex/auth.json`** (`cli_auth_credentials_store = file`) or in the OS keyring. | **NOTE**: base-secure should deny reads of `auth.json` and set the store to `keyring`. |
| 9 | §9.4: Windows `cmd /c npx` for stdio MCP | Not mentioned in the MCP docs. | **UNVERIFIED** (same as Claude Code). |
| 10 | §9.2/§9.4: `~/.codex/AGENTS.md`, `~/.codex/config.toml`, `[mcp_servers]` | All confirmed. | OK |

---

## 1. OS support and install

| OS | Support | Notes |
|---|---|---|
| macOS | CLI, IDE extension, desktop app | Sandbox: Seatbelt. Install: `curl -fsSL https://chatgpt.com/codex/install.sh \| sh` |
| Linux | CLI | Sandbox: bubblewrap + seccomp (Landlock fallback). Same installer. (The docs index also lists a *ChatGPT* desktop app for Ubuntu/Debian/Fedora/Arch; not confirmed to include Codex.) |
| Windows (native) | CLI, IDE, desktop app | Native sandbox with two modes: `elevated` (recommended, needs admin once) and `unelevated` (fallback, weaker network isolation, cannot enforce every read/write carve-out). Install: `irm https://chatgpt.com/codex/install.ps1 \| iex` (via `powershell -ExecutionPolicy ByPass`). |
| WSL | **WSL2 only.** WSL1 dropped at Codex 0.115 (Linux sandbox moved to bubblewrap). Inside WSL2, Codex uses the Linux sandbox, not the Windows one. | |

- Installer dirs: `CODEX_INSTALL_DIR` (default `~/.local/bin`, `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin` on Windows); `CODEX_NON_INTERACTIVE=1` for unattended installs.
- **NOTE (Rigfile):** the vendor's own install command is `curl | sh` / `irm | iex`, the very pattern base-secure puts behind "ask first" (§8.2). Rigfile's tool catalog should prefer a package-manager route where one exists and verify what it can; decide policy in Stage 1.

Sources: https://learn.chatgpt.com/docs/codex/cli · https://learn.chatgpt.com/docs/windows/windows-sandbox · https://learn.chatgpt.com/docs/windows/wsl · https://learn.chatgpt.com/docs/config-file/environment-variables · https://learn.chatgpt.com/docs/permissions#how-enforcement-works (2026-09-25)

---

## 2. Config roots and precedence

| Item | macOS / Linux | Windows |
|---|---|---|
| `CODEX_HOME` (state root) | `~/.codex` (env override; directory must already exist) | Docs use `~/.codex` for all OSes. Windows expansion (`%USERPROFILE%\.codex`) is **UNVERIFIED**; the permissions doc does say `~\work` works as a home-relative path on native Windows. |
| User config | `~/.codex/config.toml` | same, presumed |
| Profile overlay | `~/.codex/<profile>.config.toml`, selected with `--profile <name>` | same |
| Project config | `<repo>/.codex/config.toml` (only if the project is **trusted**) | same |
| System config | `/etc/codex/config.toml` ("on Unix") | **UNVERIFIED**: docs give no Windows path for `config.toml` |
| Enforced requirements | `/etc/codex/requirements.toml` | `%ProgramData%\OpenAI\Codex\requirements.toml` |
| macOS MDM | `com.openai.codex:requirements_toml_base64` | n/a |
| Credentials | `~/.codex/auth.json` (file) or OS keyring | same |

Precedence, highest first: CLI flags / `--config` → project `.codex/config.toml` (root → cwd, closest wins, trusted only) → profile file → user `config.toml` → cloud-managed defaults → system config → built-ins.

- **Project trust:** for an untrusted project Codex ignores project `.codex/` layers (config, hooks, rules); user and system layers still load.
- Keys that project config is warned about / ignores: `openai_base_url`, `chatgpt_base_url`, `model_provider`, `model_providers`, `notify`, `profile`, `profiles`, `otel`, and others (https://learn.chatgpt.com/docs/config-file/config-advanced#project-config-files-codexconfigtoml).
- Project root = directory containing `.git` by default; `project_root_markers` overrides.

Sources: https://learn.chatgpt.com/docs/config-file/config-basic · https://learn.chatgpt.com/docs/config-file/config-advanced · https://learn.chatgpt.com/docs/enterprise/managed-configuration (2026-09-25)

---

## 3. Instructions (AGENTS.md)

| Scope | Path |
|---|---|
| Global | `~/.codex/AGENTS.override.md` if present, **else** `~/.codex/AGENTS.md` (only the first non-empty file at this level is used) |
| Project | at each directory from project root down to cwd: `AGENTS.override.md`, then `AGENTS.md`, then names in `project_doc_fallback_filenames`; **at most one file per directory** |

- Concatenated root → cwd, blank-line separated; later (closer) files win by position.
- Size cap `project_doc_max_bytes` (default 32 KiB); later files are dropped past the cap.
- `CODEX_HOME=$(pwd)/.codex codex exec …` gives a per-project profile.
- **NOTE (merge design):** Codex uses a single global file with an override sibling. Rigfile's marked sections (`<!-- rigfile:begin id -->`) fit; also watch the 32 KiB cap (base-secure snippet + rig instructions + user text all share it). A `AGENTS.override.md` written by the user would shadow `AGENTS.md` entirely: `doctor` must detect that.

Source: https://learn.chatgpt.com/docs/agent-configuration/agents-md (2026-09-25)

---

## 4. Skills and commands

| Kind | Path |
|---|---|
| Repo skill | `$CWD/.agents/skills`, `$CWD/../.agents/skills`, … up to `$REPO_ROOT/.agents/skills` |
| **User skill** | **`$HOME/.agents/skills/<name>/SKILL.md`** |
| Admin | `/etc/codex/skills` |
| System | bundled |
| Disable without deleting | `[[skills.config]] path = "…/SKILL.md"; enabled = false` in `~/.codex/config.toml` |
| Commands (deprecated) | `~/.codex/prompts/<name>.md`, frontmatter `description`, `argument-hint`; `$FILES`-style placeholders |

- `SKILL.md` must have `name` and `description`. Codex follows the open standard (https://agentskills.io). Duplicate `name`s are **not merged**; both may show up in selectors.
- Codex shortens skill descriptions when there are many (context budget) and may omit some with a warning.
- Invocation: `$name` mention or `/skills`.
- `CODEX_HOME`'s env-var doc lists "skills" among state under that root; the skills page uses `$HOME/.agents/skills`. Treat the skills page as authoritative and confirm with a test (**UNVERIFIED**: whether `CODEX_HOME` moves skills).

Sources: https://learn.chatgpt.com/docs/build-skills#where-codex-loads-local-skills · https://learn.chatgpt.com/docs/custom-prompts · https://learn.chatgpt.com/docs/config-file/environment-variables (2026-09-25)

---

## 5. Subagents

- Built-ins: `default`, `worker`, `explorer` (a custom agent with a built-in's name takes precedence).
- Custom agents: standalone **TOML** files in `~/.codex/agents/` (personal) or `<repo>/.codex/agents/` (project, trusted only).
- Required keys: `name`, `description`, `developer_instructions`. Any other `config.toml` key may appear (`model`, `model_reasoning_effort`, `sandbox_mode`, `mcp_servers`, `skills.config`); omitted session settings inherit from the parent.
- Global knobs in `config.toml`: `[agents] enabled`, `max_concurrent_threads_per_session` (legacy `max_threads`), `default_subagent_model`, `default_subagent_reasoning_effort`, `interrupt_message`.

```toml
# ~/.codex/agents/code_reviewer.toml
name = "code_reviewer"
description = "Read-only reviewer focused on correctness and missing tests."
sandbox_mode = "read-only"
developer_instructions = """
Review the diff. Do not modify files.
"""
```

- **NOTE:** Claude Code `tools:` (allowlist) has no direct Codex equivalent; the closest are `sandbox_mode` and per-server `enabled_tools`/`disabled_tools`. Adapter must report "partial" rather than silently dropping.

Source: https://learn.chatgpt.com/docs/agent-configuration/subagents (2026-09-25)

---

## 6. MCP servers

Where: `[mcp_servers.<name>]` tables in `~/.codex/config.toml` or a trusted project's `.codex/config.toml`. Shared by CLI, IDE extension and desktop app. CLI: `codex mcp add|login|…`.

```toml
[mcp_servers.alpaca]                       # stdio
command = "npx"
args = ["-y", "@example/mcp-server@1.4.2"]
env = { EXAMPLE_PAPER = "true" }           # literal values
env_vars = ["EXAMPLE_API_KEY"]             # names to forward from Codex's own environment
startup_timeout_sec = 10
tool_timeout_sec = 60

[mcp_servers.github]                       # streamable HTTP
url = "https://mcp.example.test/mcp"
bearer_token_env_var = "EXAMPLE_TOKEN"     # env var NAME, not the value
```

- stdio keys: `command`, `args`, `env`, `env_vars`, `cwd`, `experimental_environment`.
- HTTP keys: `url`, `auth` (`oauth` default | `chatgpt`), `bearer_token_env_var`, `http_headers`, `env_http_headers`, `http_headers_helper` (local command printing a JSON header map).
- Common keys: `enabled`, `required`, `enabled_tools`, `disabled_tools`, `default_tools_approval_mode` (`auto|prompt|writes|approve`), `tools.<tool>.approval_mode`, timeouts.
- OAuth for remote servers: `codex mcp login <name>`; callback overrides exist.
- **No `${VAR}` expansion is documented** in `config.toml` (unlike Claude Code's `.mcp.json`). Secrets reach a server via `env_vars` (forwarded from Codex's environment), `bearer_token_env_var`, `env_http_headers`, or a helper command. **UNVERIFIED:** whether stdio children inherit the full parent environment by default (there is a `shell_environment_policy` key for shell commands; not checked for MCP).
- **NOTE (Rigfile):** the `rigfile exec --mcp … --` shim works unchanged (`command = "rigfile"`, `args = ["exec", …]`). For HTTP servers, `http_headers_helper` could fetch a token from the keychain without any env var: a cleaner Level-1 option than for Claude Code.
- Windows `cmd /c npx`: **UNVERIFIED** (not mentioned).
- Merge: TOML, comment preservation matters (users hand-edit `config.toml`). Rigfile must own only `[mcp_servers.<name>]` tables it created, tracked in `state.json`.

Source: https://learn.chatgpt.com/docs/extend/mcp (2026-09-25)

---

## 7. Hooks

Where (all loaded and merged; higher-precedence layers do **not** replace lower ones):
`~/.codex/hooks.json`, `~/.codex/config.toml` (`[hooks]`), `<repo>/.codex/hooks.json`, `<repo>/.codex/config.toml` (trusted only), plugin-bundled `hooks/hooks.json`, and managed `requirements.toml`. Using both forms in one layer loads both and warns.

```toml
[[hooks.PreToolUse]]
matcher = "^Bash$"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "rigfile hook pre-tool-use"
command_windows = "rigfile.exe hook pre-tool-use"   # optional Windows-only override
timeout = 30
statusMessage = "Checking command"
```

- Events documented: `SessionStart`, `SessionEnd`, `SubagentStart`, `SubagentStop`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `Stop`, `Interrupt`.
- Handlers: `command` and `mcp_tool` supported; **`prompt` and `agent` handlers are parsed but skipped.**
- Default timeout 600 s (`SessionEnd`/`Interrupt`: 1 s, max 3 s). `async = true` for background hooks.
- **Trust model:** a non-managed hook does not run until the user reviews and trusts its exact definition (hash-recorded). Changes re-trigger review. `--dangerously-bypass-hook-trust` exists for vetted automation. Managed hooks (system / MDM / cloud / `requirements.toml`) are trusted by policy and cannot be disabled by the user. Managed Windows hooks can use `windows_managed_dir` and `command_windows`.
- **NOTE (Rigfile):** for Codex, base-secure hooks need either (a) one extra manual `/hooks` trust step at the end of `pull`, listed honestly in the plan, or (b) admin-managed delivery. `doctor` should report "hook present but untrusted" as *not enforced*.
- Canonical event map (§6.1): `pre_tool_use→PreToolUse`, `post_tool_use→PostToolUse`, `stop→Stop`. Codex has no Claude-style `Notification`/`FileChanged`/… events; unsupported events → skipped with a visible note.

Source: https://learn.chatgpt.com/docs/hooks · https://learn.chatgpt.com/docs/config-file/config-advanced#hooks (2026-09-25)

---

## 8. Permissions, sandbox and approvals

Three mechanisms:

**(a) Legacy sandbox + approvals** (top-level keys)

```toml
approval_policy = "on-request"          # on-request | never | { granular = { … } }
sandbox_mode    = "workspace-write"     # read-only | workspace-write | danger-full-access
```

**(b) Permission profiles (Beta)**: *cannot be combined with (a)*. If `sandbox_mode` appears in any loaded config, the profile setting is ignored.

```toml
default_permissions = "project-edit"

[permissions.project-edit.filesystem]
":minimal" = "read"
"~/.ssh" = "deny"
"~/.aws" = "deny"
glob_scan_max_depth = 3                  # needed for unbounded ** deny globs on Linux/WSL/Windows

[permissions.project-edit.filesystem.":workspace_roots"]
"." = "write"
"**/*.env" = "deny"

[permissions.project-edit.network]
enabled = false
```

- Built-ins: `:read-only`, `:workspace`, `:danger-full-access`. Access values `read` | `write` | `deny`; `deny` beats `write` beats `read`; more specific paths override broader ones.
- Path forms: `:root`, `:minimal`, `:workspace_roots`, `:tmpdir`, `:slash_tmp`, absolute path (`C:\path` on native Windows), `~/path` (`~\path` on Windows).
- `deny` globs are supported; `read`/`write` globs are "less portable on Linux, WSL, and native Windows".
- Network domain rules only apply if `features.network_proxy = true`.
- Profiles govern **sandboxed local commands only**. They do **not** govern MCP servers, connectors, web search, browser/computer-use, or Codex service traffic. So a `deny ~/.ssh` does not stop an MCP server from reading it.
- On native Windows `unelevated` cannot enforce every split read/write carve-out; unsupported policies are **refused** (Codex fails closed rather than silently running unsandboxed).

**(c) Command rules** (experimental): `~/.codex/rules/*.rules` (also per active config layer and, for trusted projects, `<repo>/.codex/rules/`).

```python
prefix_rule(
    pattern = ["git", "push"],
    decision = "prompt",                 # allow | prompt | forbidden
    justification = "Push needs approval",
    match = ["git push origin main"],
    not_match = ["git status"],
)
```

- Most restrictive wins across matches (`forbidden` > `prompt` > `allow`); the file is validated against `match`/`not_match` at load. Prefix matching on the argv list; shell wrappers (`bash -lc "…"`) are parsed into sub-commands (see the page's section on shell wrappers).

**Canonical mapping for Rigfile `permissions:`**

| Canonical | Claude Code | Codex |
|---|---|---|
| `deny: read: <glob>` | `permissions.deny: Read(<glob>)` | `[permissions.<p>.filesystem] "<path>" = "deny"` (Beta) |
| `deny: bash: <prefix>` | `permissions.deny: Bash(<prefix> *)` | `prefix_rule(decision="forbidden")` |
| `ask: bash: <prefix>` | `permissions.ask: Bash(<prefix> *)` | `prefix_rule(decision="prompt")` |
| `allow: …` | `permissions.allow` | `prefix_rule(decision="allow")` (runs *outside* the sandbox: careful) |

- **CONFLICT/NOTE:** Codex `allow` rules run commands **outside the sandbox**, so canonical `allow` is much more dangerous on Codex than on Claude Code. The adapter should refuse to translate `allow` for Codex unless explicitly enabled per item.
- **NOTE:** Because (a) and (b) are exclusive, base-secure must pick one. Recommendation for Stage 3: use (b) only where the user has no `sandbox_mode` set; otherwise write (a) + (c) and report "path-deny not enforced".

Sources: https://learn.chatgpt.com/docs/permissions · https://learn.chatgpt.com/docs/agent-configuration/rules · https://learn.chatgpt.com/docs/agent-approvals-security · https://learn.chatgpt.com/docs/config-file/config-reference (2026-09-25)

---

## 9. Local-model settings

Documented mechanisms:

| Mechanism | Detail |
|---|---|
| `--oss` / `oss_provider` | Built-in providers `ollama` and `lmstudio` only (`--local-provider` picks per run; if neither set, the interactive CLI prompts and `codex exec` errors). |
| Custom provider | `[model_providers.<id>]`: `name`, `base_url`, `env_key`, `http_headers`, `env_http_headers`, `query_params`, `wire_api`, `[…auth]` command auth. `<id>` may **not** be `openai`, `ollama`, or `lmstudio`. Not honored from project config (`model_provider(s)` ignored there). |
| Proxy the built-in provider | `openai_base_url` in `config.toml`. |
| Select | `model_provider = "<id>"`, `model = "<name>"` |

**`wire_api`:** the configuration reference says: *"`responses` is the only supported value, and it is the default when omitted."* But the advanced-config page has an example `[model_providers.local_ollama] base_url = "http://localhost:11434/v1"` with no `wire_api`, and a note that "Chat Completions providers will ignore" `model_verbosity`. **The docs are internally inconsistent about whether Chat Completions is still supported → UNVERIFIED; must be tested with a pinned Codex version in Stage 3b.**

**Re-checked 2026-09-27 (owner decision walk-through, mlx-lm bridge question):** the config-reference page still states `wire_api` accepts `responses` only, with no mention of `chat` as a supported value. `openai/codex` discussion #7782 ("Deprecating `chat/completions` support in Codex") and issue #13628 ("`wire_api` default says 'chat' in docs but 'responses' in config schema") show this was a live, evolving inconsistency in the vendor's own docs; the deprecation's own stated hard-cutover date (February 2026) has now passed. This strengthens, but does not replace, the still-open verification step below (item 1): nothing here ran an actual pinned Codex binary. Net effect on the bridge question: unchanged conclusion, more confidently so — `mlx_lm.server` speaks Chat Completions only, custom `model_providers` need Responses, and `--oss`'s `oss_provider` is a closed `ollama | lmstudio` enum with no way to add mlx-lm to it. **Decision: defer the bridge** (a real translation server, not a config change); revisit as its own proposed milestone.

Facts about the local engines (checked 2026-09-25):
- `mlx_lm.server` (`ml-explore/mlx-lm` `main` @ `87b7b58`, 2026-09-24; PyPI latest `0.31.3`) registers only `POST /v1/completions`, `POST /v1/chat/completions` and `GET /v1/models` in `mlx_lm/server.py`. **No `/v1/responses`, no `/v1/messages`.** Defaults: `--host 127.0.0.1`, `--port 8080`, `--prompt-cache-size 10`. `--model` is optional (models can be loaded per request via the `model` field); SERVER.md warns it "only implements basic security checks" and is "not recommended for production".
- Ollama documents `POST /v1/responses` (stateless only: no `previous_response_id`/`conversation`) at https://docs.ollama.com/api/openai-compatibility and `POST /v1/messages` (Anthropic subset, names Claude Code as a client) at https://docs.ollama.com/api/anthropic-compatibility.

**Consequences for plan §9.5:**
1. **Codex ↔ `mlx_lm.server` directly does not work if Codex speaks Responses only.** It would need a bridge that translates Responses → chat completions (LiteLLM advertises a Responses bridge; **UNVERIFIED**, not researched here).
2. **Codex ↔ Ollama** is the documented, first-class route (`--oss`, `oss_provider = "ollama"`).
3. Claude Code ↔ local model is vendor-unsupported (see `claude-code.md` §9); Ollama's Anthropic endpoint is the only documented candidate.
4. Rigfile's "reference setup" (mlx-lm + Qwen3-8B-4bit) is therefore **not reachable from either agent without a bridge**. The plan's Stage 3b exit criterion ("Codex + Claude Code (via a subagent) successfully use it") must be re-scoped or the bridge made explicit and tested.

Sources: https://learn.chatgpt.com/docs/config-file/config-advanced#custom-model-providers · https://learn.chatgpt.com/docs/config-file/config-reference · https://raw.githubusercontent.com/ml-explore/mlx-lm/main/mlx_lm/server.py · https://raw.githubusercontent.com/ml-explore/mlx-lm/main/mlx_lm/SERVER.md · https://docs.ollama.com/api/openai-compatibility · https://docs.ollama.com/api/anthropic-compatibility (2026-09-25)

---

## 10. Credentials and logins

- `codex login` (browser sign-in; API key; `CODEX_API_KEY` for `exec`; `codex login --with-access-token`).
- `cli_auth_credentials_store = "file" | "keyring" | "auto" | "ephemeral"`; `file` writes `~/.codex/auth.json` (plaintext).
- **NOTE (Rigfile):** base-secure for Codex should set `cli_auth_credentials_store = "keyring"` (where a keyring exists) and deny reads of `~/.codex/auth.json`. Headless Linux has no keyring: `auto` semantics must be checked (**UNVERIFIED**).

Source: https://learn.chatgpt.com/docs/auth#credential-storage (2026-09-25)

---

## 11. Open items for Stage 1/3 (Codex)

1. Confirm `wire_api = "chat"` still works (or not) on a pinned Codex version.
2. Confirm Windows expansion of `CODEX_HOME` / `~` and the system `config.toml` path on Windows.
3. Test `env_vars` vs full-environment inheritance for stdio MCP servers.
4. Decide policy for hook trust (manual step vs managed delivery).
5. Decide between permission profiles (Beta) and legacy sandbox keys for base-secure.
6. Check whether `CODEX_HOME` relocates `$HOME/.agents/skills`.
