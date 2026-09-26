# Target: Claude Code

**Date checked:** 2026-09-25 (all sources below were fetched on this date)
**Method:** Official docs at `code.claude.com/docs/en/*`, read as raw markdown (append `.md` to any page URL). Only public documentation was used. Nothing was read from any real `~/.claude` directory.
**Legend:** **CONFLICT** = docs disagree with `RIGFILE_PLAN.md`. **UNVERIFIED** = docs are silent or unclear; needs a real test in Stage 1 before an adapter relies on it. **NOTE** = a fact that changes adapter design.
**Version signal:** the docs reference features up to Claude Code v2.1.277 (AGENTS.md reading). The docs do not publish a stable "current version" on these pages; adapters must feature-detect (`claude --version`) rather than assume.

All snippets use obviously fake values.

---

## 0. Summary of plan corrections (Claude Code)

| # | Plan says | Docs say | Status |
|---|---|---|---|
| 1 | §9.2: user MCP servers in `~/.claude.json` / project `.mcp.json` ("verify") | Confirmed. Three scopes: local and user both live in `~/.claude.json`; project in `.mcp.json`. But `~/.claude.json` is a file Claude Code **writes itself** (sign-in session, per-project state, trust decisions). | **NOTE**: do not blind-merge into it. See §5. |
| 2 | §9.2: commands in `~/.claude/commands/*.md` | Still works, but "custom commands have been merged into skills". Both create `/name`. | **NOTE**: prefer emitting skills. |
| 3 | §6.1 hook `event: pre_tool_use` | Real event names are PascalCase (`PreToolUse`, `PostToolUse`, `Stop`, …) and there are 30+ of them. Handler types: `command`, `http`, `mcp_tool`, `prompt`, `agent`. | Needs a canonical-to-native event table (§7). |
| 4 | §9.4/§9.2: Windows needs `cmd /c npx …` for stdio MCP | **Not mentioned anywhere** in the MCP, settings or setup docs. The hooks doc has the analogous rule for *hooks*: on Windows, exec-form hooks cannot spawn `.cmd`/`.bat` shims. | **UNVERIFIED** for MCP. Test on a clean Windows VM. |
| 5 | §8.2: `~/.claude/**` credentials not listed in deny list | On Linux and Windows, login credentials are stored in **plaintext** `~/.claude/.credentials.json` (0600 on Linux; profile ACL on Windows). macOS uses Keychain. | **NOTE**: add `~/.claude/.credentials.json` and `~/.claude.json` to base-secure read-deny. |
| 6 | §9.5: point `ANTHROPIC_BASE_URL` at LiteLLM to reach a local OpenAI-style model | Docs: Anthropic "doesn't support routing Claude Code to non-Claude models through any gateway". The gateway must expose the Anthropic Messages format (or Bedrock/Vertex formats). | **CONFLICT** (unsupported by the vendor; may work technically). See §9. |
| 7 | §9.5: "scope local models to specific subagents" | Subagent `model` field picks a model *ID* but `ANTHROPIC_BASE_URL` is process-wide. No per-subagent endpoint is documented. | **UNVERIFIED**; likely only achievable if the gateway routes by model name and *all* traffic goes through it. |
| 8 | §8.2: deny `Read(...)` protects secrets | Read/Edit deny rules cover built-in file tools and *recognized* Bash commands (`cat`, `head`, `sed`, redirects…) but **not** scripts or subprocesses that open files themselves. OS-level enforcement needs the sandbox, which is **not available on native Windows**. | **NOTE**: on native Windows base-secure's file protection is weaker; the plan screen must say so. |
| 9 | §8.2: `~/.ssh/**` style paths | Read/Edit rules use **gitignore syntax** with special anchors (`//abs`, `~/`, `/` = relative to settings file). On Windows, paths are normalized to POSIX form (`C:\Users\a` → `/c/Users/a`). | **NOTE**: "write once, expand per OS" must emit different *syntax*, not just different paths. See §8. |

---

## 1. OS support

| OS | Supported | Notes |
|---|---|---|
| macOS | 13.0+ | |
| Linux | Ubuntu 20.04+, Debian 10+, Alpine 3.19+ | Alpine/musl needs `bash`, `curl`, `libgcc`, `libstdc++`, `ripgrep`. Install also via apt/dnf/apk repos. |
| Windows | 10 1809+ / Server 2019+ | Native or WSL. Git for Windows is **optional**: with it, the Bash tool uses Git Bash; without it, Claude Code uses the PowerShell tool. |
| WSL | WSL 2 supported; WSL 1 works for the CLI but not sandboxing | Sandbox supported on macOS, Linux, WSL2 only. |

Sources: https://code.claude.com/docs/en/setup · https://code.claude.com/docs/en/sandboxing (2026-09-25)

---

## 2. Config roots and precedence

| Item | macOS | Linux | Windows | WSL |
|---|---|---|---|---|
| User dir (`~/.claude`) | `~/.claude` | `~/.claude` | `%USERPROFILE%\.claude` | inside WSL: `~/.claude` (Windows-side is a separate install) |
| Override | `CLAUDE_CONFIG_DIR` (moves settings, history, plugins, `.credentials.json`) | same | same | same |
| Global app state | `~/.claude.json` | `~/.claude.json` | `%USERPROFILE%\.claude.json` (**UNVERIFIED**: docs say "`~/.claude` means `%USERPROFILE%\.claude` on Windows"; the `.claude.json` sibling is implied, not stated) | |
| Managed settings dir | `/Library/Application Support/ClaudeCode/` | `/etc/claude-code/` | `C:\Program Files\ClaudeCode\` | `/etc/claude-code/` (Windows policy only if `wslInheritsWindowsSettings` is set) |
| Managed alternatives | MDM profile domain `com.anthropic.claudecode` | n/a | HKLM/HKCU `SOFTWARE\Policies\ClaudeCode` value `Settings` | |

Settings precedence (highest first): managed → `--settings` CLI → `.claude/settings.local.json` → `.claude/settings.json` → `~/.claude/settings.json`.
Managed dir also supports `managed-settings.json`, `managed-settings.d/*.json` (merged alphabetically), `managed-mcp.json`, and `CLAUDE.md`.

**NOTE (Rigfile):** writing to the managed directory needs admin rights, which the plan forbids doing implicitly (§9.4 "Admin rights"). base-secure must therefore live in *user* settings; a user can override it unless an admin deploys managed settings. Say so on the plan screen ("enforced by user-level settings only").

Sources: https://code.claude.com/docs/en/settings · https://code.claude.com/docs/en/managed-settings · https://code.claude.com/docs/en/claude-directory (2026-09-25)

---

## 3. Instructions (CLAUDE.md)

| Scope | Path (all OSes; `~` = `%USERPROFILE%` on Windows) |
|---|---|
| User | `~/.claude/CLAUDE.md` |
| Project | `./CLAUDE.md` or `./.claude/CLAUDE.md` |
| Project-local (gitignored) | `./CLAUDE.local.md` |
| Rules | `~/.claude/rules/*.md` and `.claude/rules/*.md` (recursive; optional `paths:` frontmatter to scope by glob) |
| Managed | `/Library/Application Support/ClaudeCode/CLAUDE.md` · `/etc/claude-code/CLAUDE.md` · `C:\Program Files\ClaudeCode\CLAUDE.md` |

- Format: Markdown. Imports via `@path/to/file` (relative to the importing file, max depth 4, skipped inside code spans/fences).
- Load order: all discovered files are concatenated root → cwd; nothing overrides.
- **AGENTS.md:** Claude Code reads a project `AGENTS.md` **only** when no `CLAUDE.md`/`.claude/CLAUDE.md`/`CLAUDE.local.md` exists at or above the working directory (requires v2.1.277+), unless the `Project instructions` setting is changed. It does **not** read `~/.codex/AGENTS.md`, `AGENTS.local.md`, `AGENTS.override.md`, or `.agents/`.
- **NOTE (merge design):** Rigfile's marked-section approach (`<!-- rigfile:begin id -->`) works because CLAUDE.md is plain Markdown. Better for user scope: write a Rigfile-owned file `~/.claude/rules/rigfile-<id>.md` (or a `~/.claude/rigfile/<id>.md` imported with `@`) instead of editing the user's own `CLAUDE.md`. **Open design question**, both are viable.

Source: https://code.claude.com/docs/en/memory (2026-09-25)

---

## 4. Skills and commands

| Kind | Path |
|---|---|
| User skill | `~/.claude/skills/<name>/SKILL.md` |
| Project skill | `.claude/skills/<name>/SKILL.md` |
| Nested project | `<subdir>/.claude/skills/<name>/SKILL.md` |
| Plugin | `<plugin>/skills/<name>/SKILL.md` (invoked `/plugin:skill`) |
| User command (legacy, still supported) | `~/.claude/commands/<name>.md` |
| Project command | `.claude/commands/<name>.md` |

- `SKILL.md`: YAML frontmatter + Markdown. Fields: `name`, `description`, `when_to_use`, `disable-model-invocation`, `user-invocable`, `argument-hint`, `arguments`, `allowed-tools`, `disallowed-tools`, `model`, `effort`, `context` (`fork`), `agent`, `background`, `hooks`, `paths`, `shell` (`bash`|`powershell`), `metadata`, `license`, `compatibility`.
- Commands accept the same fields except `name` and `paths`. Command and skill with the same name both create `/name`.
- Reserved: a skill folder named `synced` (any case).
- Skills are live-reloaded within a session.
- **Windows:** `shell: bash` skills fail without Git Bash; use `shell: powershell` or ship OS-neutral scripts. `${CLAUDE_SKILL_DIR}` / `${CLAUDE_PROJECT_DIR}` are the portable path variables.
- **NOTE:** frontmatter includes Claude-only fields (`allowed-tools`, `context`, `hooks`, …). A Rigfile skill that is meant to be portable to Codex should stay within the open Agent Skills fields (`name`, `description`, `license`, `compatibility`, `metadata`) or carry per-target overrides.

Sources: https://code.claude.com/docs/en/skills · https://code.claude.com/docs/en/slash-commands (2026-09-25)
*Correction to method:* a summarizing fetch of the slash-commands page once reported "project: `.claude/skills/`" for commands; the raw page and the `claude-directory` reference both say `.claude/commands/`. Raw pages were used for everything here.

---

## 5. MCP servers

| Scope | Stored in | Shared | Precedence |
|---|---|---|---|
| Local (default) | `~/.claude.json` → `projects["<abs path>"].mcpServers` | no | 1 (highest) |
| Project | `<project>/.mcp.json` → `mcpServers` | yes (VCS) | 2 |
| User | `~/.claude.json` → top-level `mcpServers` | no | 3 |
| Plugin / claude.ai connectors | — | — | 4, 5 |
| Managed | `managed-mcp.json` or `managedMcpServers` setting | org | above all |

- Same-name servers: the **whole entry** from the highest scope wins; no field merging.
- Entry shapes:
  - stdio: `{ "command": "npx", "args": ["-y", "pkg@1.2.3"], "env": { "K": "${K}" } }` (optional `"type": "stdio"`)
  - http: `{ "type": "http", "url": "https://…", "headers": { … } }`
  - also `sse` (legacy) and `ws`.
- **Env expansion in `.mcp.json`:** `${VAR}` and `${VAR:-default}` in `command`, `args`, `env`, `url`, `headers`. Unset variable without default → config still loads and the literal `${VAR}` is used (warning in `claude mcp list`).
- **Credential variables read as empty** in a *remote* server's `url`/`headers` (e.g. `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `AWS_BEARER_TOKEN_BEDROCK`, `HTTPS_PROXY`, `NPM_TOKEN`). Other names expand as written.
- Dynamic headers (a helper that produces auth headers) exist: https://code.claude.com/docs/en/mcp#use-dynamic-headers-for-custom-authentication
- Project-scoped servers need per-server user approval in interactive sessions (`claude mcp reset-project-choices`); they load without asking in `-p`, SDK and cloud sessions.
- CLI: `claude mcp add [--transport stdio|http|sse|ws] [--scope local|project|user] [--env K=V] name -- cmd args…`, `claude mcp add-json`, `claude mcp list|get|remove`.
- Import from Claude Desktop: **macOS and WSL only**.
- **Windows stdio (`cmd /c npx`)**: **UNVERIFIED**, not in the docs (see corrections #4).

**NOTE (Rigfile design):**
1. `~/.claude.json` mixes MCP config with volatile app state and the login session. Two safe options: (a) shell out to `claude mcp add-json --scope user` (the vendor's own writer, race-safe), or (b) write a project `.mcp.json` when the rig is project-scoped. Direct JSON merge into `~/.claude.json` while Claude Code is running risks lost updates. **Decision needed in Stage 1.**
2. Secrets: the plan's `rigfile exec --mcp` shim (§7.2 Level 1) is still valid. But `.mcp.json`'s `${VAR}` expansion is a documented, simpler mechanism for env-based secrets (value comes from the shell environment, not the file). It requires the variable to be present in Claude Code's own environment, which then leaks it to every child process, so the shim remains the tighter option.

Source: https://code.claude.com/docs/en/mcp (2026-09-25)

---

## 6. Subagents

| Scope | Path | Priority |
|---|---|---|
| Managed | `.claude/agents/` in managed dir | 1 |
| CLI | `--agents '<json>'` (session only) | 2 |
| Project | `.claude/agents/*.md` | 3 |
| User | `~/.claude/agents/*.md` | 4 |
| Plugin | `<plugin>/agents/` | 5 |

- Markdown + YAML frontmatter. Required: `name`, `description`. Optional: `tools`, `disallowedTools`, `model` (`sonnet`|`opus`|`haiku`|`fable`|full ID|`inherit`), `permissionMode`, `maxTurns`, `skills`, `mcpServers`, `hooks`, `memory`, `background`, `effort`, `isolation`, `color`, `initialPrompt`, `omitClaudeMd`, `experimental`.
- Directories scanned recursively; identity is the `name` field, not the file name. Files without `name`+`description`, or not starting with `---`, are silently skipped.
- Model resolution: per-invocation → frontmatter → `CLAUDE_CODE_SUBAGENT_MODEL` → main model.

Source: https://code.claude.com/docs/en/sub-agents (2026-09-25)

---

## 7. Hooks

Where: `hooks` key in any settings file (`~/.claude/settings.json`, `.claude/settings.json`, `.claude/settings.local.json`, managed), plugin `hooks/hooks.json`, skill/subagent frontmatter.

Shape (three levels: event → matcher group → handlers):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "if": "Bash(git commit *)", "command": "rigfile", "args": ["hook", "pre-commit-guard"], "timeout": 30 }
        ]
      }
    ]
  }
}
```

- Handler types: `command` | `http` | `mcp_tool` | `prompt` | `agent`.
- **Exec form vs shell form:** with `args`, `command` is spawned directly (no shell, same on all OSes). Without `args`, it goes through `sh -c` (macOS/Linux), Git Bash, or PowerShell (Windows without Git Bash); `shell: "bash"|"powershell"` selects explicitly. On Windows, exec form needs a real `.exe`; `.cmd`/`.bat` shims (npm, npx) need shell form.
  **NOTE:** this makes `builtin:` hooks (a `rigfile.exe hook …` binary) the right portable choice: `{"command":"rigfile","args":["hook","…"]}` works identically on all three OSes.
- Hooks merge (union) across scopes; identical handlers run once; all matching hooks run in parallel; a user cannot remove managed hooks (`allowManagedHooksOnly` can disable user hooks entirely).
- Exit code 2 blocks; JSON output can return `permissionDecision`. A blocking hook wins over allow rules; deny/ask **rules still apply regardless** of hook output.
- The `if` filter is documented as "best-effort"; for hard allow/deny use permissions.
- Canonical → native event map (Rigfile §6.1 `event:`): `pre_tool_use→PreToolUse`, `post_tool_use→PostToolUse`, `stop→Stop`, plus `SessionStart`, `UserPromptSubmit`, `SubagentStop`, `PreCompact`, `Notification`, `SessionEnd`, … (full list: https://code.claude.com/docs/en/hooks#hook-events).

Source: https://code.claude.com/docs/en/hooks (2026-09-25)

---

## 8. Permissions

Where: `permissions` object in settings: `allow`, `ask`, `deny` (arrays of rule strings), `additionalDirectories`, `defaultMode`, `blockReadsOutsideWorkingDirectories`, `disableBypassPermissionsMode`, `disableAutoMode`.

```json
{
  "permissions": {
    "deny": ["Read(./.env)", "Read(~/.ssh/**)", "Bash(git push --force *)"],
    "ask":  ["Bash(git push *)"],
    "allow": ["Bash(npm run *)"]
  }
}
```

- Evaluation order: **deny → ask → allow**, first match wins; a deny in *any* scope beats an allow in any other. This matches the plan's "deny always wins" (§6.3).
- Rule syntax: `Tool` or `Tool(specifier)`. Tools include `Bash`, `PowerShell`, `Read`, `Edit`, `WebFetch(domain:…)`, `mcp__server__tool`, `Agent(…)`, `Cd(…)`. `Read` deny also blocks Edit/Write on the path. Path rules for `Write`/`Glob`/`NotebookEdit` are accepted but never consulted (use `Edit`/`Read`).
- **Path anchors (Read/Edit)**, gitignore-style:
  - `./x` or `x`: relative to the current directory
  - `/x`: relative to the *settings source*, **not** the filesystem root: `<primary working directory>/x` in project/local settings, `~/.claude/x` in user settings, `<dir of file>/x` for `--settings <file>`
  - `~/x`: home-relative
  - `//x`: absolute from filesystem root
  - Windows: paths are normalized to POSIX (`C:\Users\alice` → `/c/Users/alice`); use `//c/**/.env`, or `//**/.env` for all drives.
  - **NOTE:** a deny in user settings written as `Read(/secrets/**)` blocks `~/.claude/secrets/**`, not the project's. Adapters must always emit `~/` or `//` forms for user-level denies.
- Bash rules match on command prefixes/wildcards; docs explicitly warn argument-constraining patterns are fragile (`curl` example). Wrapper commands, `sh -c`, absolute paths can evade simple patterns.
- Enforcement gap (repeat of corrections #8): deny rules don't cover a script that opens files itself; the sandbox (macOS/Linux/WSL2, **not native Windows**) is the OS-level layer.

Sources: https://code.claude.com/docs/en/permissions · https://code.claude.com/docs/en/settings-reference#permissions (2026-09-25)

---

## 9. Local-model settings

| Need | Documented mechanism |
|---|---|
| Custom endpoint | `ANTHROPIC_BASE_URL` (env var; can be set in settings `env`). Points **all** requests at a gateway. |
| Auth to gateway | `ANTHROPIC_AUTH_TOKEN` (sent as `Authorization: Bearer …`), or `apiKeyHelper`. While a gateway credential is active the claude.ai subscription is not used. Setting only `ANTHROPIC_BASE_URL` keeps the saved login as the active credential. |
| Pin model IDs | `ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL`, `ANTHROPIC_CUSTOM_MODEL_OPTION`, `CLAUDE_CODE_SUBAGENT_MODEL` (+ `_FORCE`) |
| Gateway model list | `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1` (reads `/v1/models`) |
| Supported gateway API formats | Anthropic Messages (`/v1/messages`), Bedrock InvokeModel, Vertex rawPredict. **No OpenAI-format option.** |

Other effects of a non-first-party `ANTHROPIC_BASE_URL`: MCP tool search is disabled by default (set `ENABLE_TOOL_SEARCH=true` if the proxy forwards `tool_reference` blocks); Remote Control is disabled (v2.1.196+).

**CONFLICT with plan §9.5:**
- "Anthropic doesn't support routing Claude Code to non-Claude models through any gateway." A LiteLLM bridge to `mlx_lm.server` is therefore *unsupported by the vendor*. It may work; treat as experimental and say so on the plan screen.
- The Anthropic-native route that *is* documented by the local-engine side: Ollama implements `/v1/messages` and lists Claude Code as a client (https://docs.ollama.com/api/anthropic-compatibility, 2026-09-25). That is a third-party doc, not Anthropic's.
- Per-subagent local models are **UNVERIFIED** (see corrections #7).

Sources: https://code.claude.com/docs/en/gateways · https://code.claude.com/docs/en/llm-gateway · https://code.claude.com/docs/en/llm-gateway-protocol · https://code.claude.com/docs/en/env-vars · https://code.claude.com/docs/en/model-config (2026-09-25)

---

## 10. Credentials and logins (relevant to base-secure and `rigfile login`)

| OS | Where Claude Code keeps its login |
|---|---|
| macOS | Keychain (falls back to `~/.claude/.credentials.json`, 0600, if the Keychain write is rejected e.g. locked over SSH) |
| Linux | `~/.claude/.credentials.json`, 0600 |
| Windows | `%USERPROFILE%\.claude\.credentials.json` (inherits profile ACL) |
| Custom | under `CLAUDE_CONFIG_DIR` if set (also keys the macOS Keychain entry) |

Also: transcripts under `~/.claude/projects/<project>/<session>.jsonl` are **plaintext** and contain anything a tool printed (including secrets). Relevant settings: `cleanupPeriodDays`, `CLAUDE_CODE_SKIP_PROMPT_HISTORY`. This is a real leak path for Rigfile's "secrets never in agent-readable files" claim: a secret that appears in tool output is persisted in the transcript. The base-secure post-tool redactor (§8.2) is the right mitigation but only covers what hooks can rewrite. **Add to threat model.**

Source: https://code.claude.com/docs/en/authentication#credential-management · https://code.claude.com/docs/en/claude-directory#application-data (2026-09-25)

---

## 11. Open items for Stage 1 (Claude Code)

1. Decide the write path for user-scope MCP (CLI vs direct JSON) and instructions (marked section vs owned rules file).
2. Test on a clean Windows VM: stdio MCP with `npx` with and without `cmd /c`.
3. Verify whether a subagent can be routed to a different endpoint than the main session.
4. Confirm `%USERPROFILE%\.claude.json` location on Windows.
5. Feature-detect version-gated behavior (AGENTS.md ≥ 2.1.277, `managedMcpServers` ≥ 2.1.259, `${VAR}` display ≥ 2.1.268).

---

## 12. Stage 2 verification (base-secure), checked 2026-09-25

Method: official pages fetched as markdown (`code.claude.com/docs/en/{hooks,permissions,permission-modes,sandboxing}.md`); answers were extracted by a summarising fetch, so quoted text is as returned and the **hooks page excerpt was truncated**: items marked **TEST** must be confirmed against a real Claude Code (S2-M7 live procedure) before we claim them in user-facing text. Nothing was read from any real `~/.claude`.

### 12.1 Can a hook redact tool output? (question S2-M0(a))

**Yes for built-in tools.** PostToolUse supports `updatedToolOutput`, `updatedMCPToolOutput`, `additionalContext`, `systemMessage`, `terminalSequence`. Quote: *"For all tools except MCP tools, `updatedToolOutput` replaces the tool's text output before it reaches the model. For MCP tools, use `updatedMCPToolOutput` instead."* `updatedMCPToolOutput` needs v2.1.256+. **UNVERIFIED:** the minimum version for `updatedToolOutput`, and whether the on-disk transcript (`~/.claude/projects/**/*.jsonl`, §10) stores the original or the replaced output. **TEST** both. PostToolUseFailure cannot rewrite output (context/message only).
Consequence: the plan's §8.2 "redact secret-looking strings before they enter the model context" is feasible for Bash/Read/etc.; keep the `redact` hook in S2-M5, feature-detect by version, and say on the plan screen if the transcript still holds the raw text.

### 12.2 PreToolUse contract

Output: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow|deny|ask|defer","permissionDecisionReason":"…"}}`, or exit 2 + stderr. Exit 2 blocks and overrides JSON; other non-zero exits do not block for most events (so **malformed/crashing hooks fail open unless they exit 2**; the Stage 1 guard already exits 2 on malformed input). `updatedInput` may rewrite any tool's input. Hooks also run inside subagents (`agent_id`, `agent_type` in the payload). `permission_mode` is in the payload (`default|plan|acceptEdits|auto|dontAsk|bypassPermissions`).
Tool inputs (as returned by the summary; **TEST** the Write/Edit field names, they differ from the ones our guard reads today): Bash `{command, description, timeout, run_in_background}`; Write `{file_path, file_text}`; Edit `{file_path, old_str, new_str}`; Read `{file_path}`.
`if`: exactly one permission rule per handler (no `&&`/`||`), only on tool events, "best-effort"; docs say to use permissions rather than hooks for hard allow/deny.
**Hooks can be turned off:** `disableAllHooks: true` in any settings file or `--settings` on the command line; managed hooks are immune unless disabled at managed level. So a user-level hook is a *guardrail against agent mistakes*, not a control against a determined local user.

### 12.3 Permissions: what a deny actually covers

- Order deny → ask → allow; a deny beats a more specific allow ("An allow rule can't carve an exception out of a deny rule").
- Bash rules match the command text after splitting compound commands (`&&`, `||`, `;`, `|`, `|&`, `&`, newline); **deny and ask apply if any subcommand matches, including inside subshells, `$(…)` and loop bodies**. A fixed set of wrappers is stripped before matching (`timeout`, `time`, `nice`, `nohup`, `stdbuf`, `command`, `builtin`, `noglob`; bare `xargs`); `npx`, `docker exec`, `env`-style runners are not. Deny also matches past leading `VAR=x` assignments.
- **Documented gaps** (docs' own table): `Bash(curl *)` does not stop `/usr/bin/curl …` or `sh -c 'curl …'`; `Bash(rm *)` does not stop `/bin/rm` or `bash -c 'rm …'`; `Bash(git push *)` does not stop `git -C . push`, `git -c push.default=current push`, `git 'push'`. The docs call these rules "not a security boundary around the program". → **base-secure denies must be written with the variants, and the PreToolUse guard (which sees the full text) must parse `git -C/-c` and path-qualified programs; hooks are the second layer, the sandbox the third.**
- `Bash(command:rm *)` style rules are ignored with a warning (bypassable), use `Bash(rm *)`.
- Read/Edit deny applies to built-in file tools, to recognised Bash file commands (`cat head tail sed tee`) and redirect targets, best-effort to Grep/Glob, `@file` mentions and IDE context. It does **not** apply to `grep -r` run in the directory holding the file, or to scripts that open files themselves (python/node). OS-level enforcement = the sandbox.
- Paths in user settings: `/x` means `~/.claude/x`; use `~/` or `//abs` (already handled by the adapter).
- `Bash(*)`/bare `Bash` as a deny removes the tool. `Bash(dangerouslyDisableSandbox:true)` can be given an **ask** rule so unsandboxed retries always prompt.

### 12.4 Protected paths (agent cannot silently edit these)

Never auto-approved for writes (prompted in `default`/`acceptEdits`, classifier in `auto`, denied in `dontAsk`, **allowed in `bypassPermissions`**), and settings `allow` rules do not pre-approve them: dirs `.git`, `.config/git`, `.vscode`, `.idea`, `.husky`, `.cargo`, `.devcontainer`, `.yarn`, `.mvn`, `.claude` (except `.claude/worktrees`); files `.gitconfig`, `.gitmodules`, shell rc files (`.bashrc .zshrc .profile .envrc …`), `.npmrc`, `.pre-commit-config.yaml`, `lefthook*.yml`, `.mcp.json`, `.claude.json`, and more.
**Consequences for Rigfile:** (1) `~/.gitconfig` and `~/.config/git/**` (the owner's current hooks dir) are protected, which helps; (2) **Rigfile's own dirs (`~/.config/rigfile/**`, `~/.rigfile/**`) are NOT on the list**, so base-secure must add `Edit(~/.config/rigfile/**)`, `Edit(~/.rigfile/**)` and `Read(~/.rigfile/secrets*)` to its deny list; (3) `bypassPermissions` skips protected-path prompts, so base-secure should set `permissions.disableBypassPermissionsMode: "disable"` (it can be set in user settings; **TEST** that user scope is honoured and whether this is too heavy-handed, see plan decision O8). `rm`/`rmdir` on "critical paths" (root, home, cwd and parents…) can never be approved by an allow rule or hook; a deny still blocks.

### 12.5 The sandbox is a bigger lever than the plan assumed (**new finding**)

Sandbox: OS-enforced filesystem and network isolation for Bash/PowerShell/Monitor commands and their children; macOS (Seatbelt, nothing to install), Linux and WSL2 (needs `bubblewrap` + `socat`-style packages; see the page), **not native Windows**. Configurable in **user settings**: `sandbox.enabled`, `sandbox.failIfUnavailable` (default: warns and runs *unsandboxed* if it cannot start), `sandbox.allowUnsandboxedCommands: false` (disables the `dangerouslyDisableSandbox` retry), `sandbox.filesystem.{allowWrite,denyWrite,allowRead,denyRead}` (narrower path wins; wildcard deny holds inside a wider allow), `sandbox.network.allowedDomains`, and `sandbox.credentials.files` / env-var `mask` entries with an injecting proxy (only honoured from user/managed/`--settings`, never from repo settings).
**Impact on the plan:** the OS-level layer that §8.2/§11 defer to Stage 7 (egress allowlist, secret surrogates) partly exists in the vendor product for Claude Code. **Not verified:** exact enforcement gaps, Linux dependencies on a stock Ubuntu/Fedora, interaction with MCP servers and hooks (hooks are not sandboxed Bash). Recommendation in `docs/stage-2-plan.md` (O7): research and prototype in Stage 2 as an *optional, plan-screen-visible* layer of base-secure on macOS/Linux; do not depend on it for exit criteria.

Sources: https://code.claude.com/docs/en/hooks · /permissions · /permission-modes · /sandboxing (2026-09-25)

### 12.6 S2-M5 addenda (2026-09-25)

- **PostToolUse output shape:** the Agent SDK hooks page (`code.claude.com/docs/en/agent-sdk/hooks`) says: for `PostToolUse`, inside **`hookSpecificOutput`** set `additionalContext` to append to the tool result, or **`updatedToolOutput` to replace the tool's output before Claude sees it, which "works for any tool"**; `updatedMCPToolOutput` is deprecated. The value type and the `tool_response` field shapes are still not documented in the pages the fetcher could read (the reference page is truncated): **UNVERIFIED**, handled by reading several shapes and confirmed in the live procedure.
- **Hook matcher for edits:** `Edit`, `MultiEdit`, `NotebookEdit` are separate tool names; base-secure matches `Edit|MultiEdit|NotebookEdit`. Write/Edit `tool_input` field names differ between sources (`content`/`new_string` vs `file_text`/`new_str`); the write guard reads all of them.
