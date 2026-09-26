# Target support matrix (generated)

Generated from `internal/targets/capabilities/*.yaml` by `go test ./internal/targets -update`; do not edit by hand. ✔ = supported, ◐ = partial (see note), ✘ = not supported (the plan screen says so; nothing is dropped silently).

| Category | Claude Code | Claude Desktop | Codex CLI | Cursor | Gemini CLI |
|---|---|---|---|---|---|
| instructions | ✔ marked section in CLAUDE.md | ✘ not supported by Claude Desktop | ✔ marked section in ~/.codex/AGENTS.md | ◐ no user rules file (UI only): printed for pasting; project rules go to .cursor/rules/*.mdc | ✔ marked section in ~/.gemini/GEMINI.md |
| skills | ✔ ~/.claude/skills/<name> | ✘ not supported by Claude Desktop | ✔ ~/.agents/skills (open Agent Skills format) | ✘ not covered by the documentation read | ✘ not documented for Gemini CLI |
| agents | ✔ ~/.claude/agents/*.md | ✘ not supported by Claude Desktop | ✔ TOML files in ~/.codex/agents | ✘ not covered by the documentation read | ✘ not documented for Gemini CLI |
| commands | ✔ ~/.claude/commands/*.md | ✘ not supported by Claude Desktop | ◐ custom prompts are deprecated; commands become skills | ✘ not covered by the documentation read | ✔ TOML files in ~/.gemini/commands |
| mcp servers | ✔ claude mcp add-json --scope user (ADR 0002) | ◐ local stdio servers in claude_desktop_config.json; remote connectors are added in the app | ✔ [mcp_servers.<name>] in ~/.codex/config.toml | ✔ ~/.cursor/mcp.json | ✔ mcpServers in ~/.gemini/settings.json |
| hooks | ✔ settings.json hooks; built-in rigfile hooks | ✘ not supported by Claude Desktop | ◐ hooks need the user to review and trust them (hash-pinned); planned but not auto-trusted | ✘ UNVERIFIED; skipped | ✘ a hooks system exists but its settings contract is UNVERIFIED; skipped |
| permissions | ✔ settings.json permissions deny/ask/allow | ✘ not supported by Claude Desktop | ◐ sandbox_mode / approval_policy, not per-rule deny; base-secure maps what it can | ✘ no documented permission rules | ◐ tools.exclude / confirmationRequired; rule syntax UNVERIFIED |
| runs on | macos, linux, windows | macos, windows | macos, linux, windows | macos, linux, windows | macos, linux, windows |
| config dir | macos: `~/.claude`<br>linux: `~/.claude`<br>windows: `%USERPROFILE%\.claude` | macos: `~/Library/Application Support/Claude`<br>windows: `%APPDATA%\Claude` | macos: `~/.codex`<br>linux: `~/.codex`<br>windows: `%USERPROFILE%\.codex` | macos: `~/.cursor`<br>linux: `~/.cursor`<br>windows: `%USERPROFILE%\.cursor` | macos: `~/.gemini`<br>linux: `~/.gemini`<br>windows: `%USERPROFILE%\.gemini` |
| base-secure | enforced (permissions + hooks) | not applicable | partly enforced, rest instructions only | instructions only, not enforced | instructions only, not enforced |
