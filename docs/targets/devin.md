# Target: Devin (formerly Windsurf) (S8-M6 + owner decision 2026-09-27) — BUILT (partial)

**Date checked:** 2026-09-26 (research), 2026-09-27 (owner confirmation on their own machine, resolving the conflict below).

| Category | Finding | Status |
|---|---|---|
| MCP config path | `https://docs.windsurf.com/plugins/cascade/mcp` (found by search) gives `~/.codeium/windsurf/mcp_config.json`. The older URL `https://docs.windsurf.com/windsurf/cascade/mcp` now **redirects (307) to `https://docs.devin.ai/desktop/cascade/mcp`**, whose page gives **`~/.config/devin/mcp_config.json`** (or `$XDG_CONFIG_HOME/devin/...`) on macOS and Linux and `%APPDATA%\devin\mcp_config.json` on Windows. **Resolved 2026-09-27**: the owner added an MCP server through the installed app's own UI and confirmed, on their own machine, that it writes `~/.config/devin/mcp_config.json` with a top-level `mcpServers` key. The app is Devin, not Windsurf — the product has rebranded. | **Confirmed on macOS.** Linux path is the same vendor doc, not independently run. Windows (`%APPDATA%\devin\mcp_config.json`) is from the docs only. |
| MCP JSON shape | Top-level `mcpServers`; stdio `command`, `args`, `env`; remote `serverUrl` or `url`, `headers`. Interpolation in `command`, `args`, `env`, `serverUrl`, `url`, `headers`: `${env:VAR_NAME}` and `${file:/path}` (trimmed file contents). | Consistent between the pages read, and the top-level key matches the owner's real file. The per-entry field names were **not** re-checked against a live populated entry (the owner's file was empty when confirmed: `{"mcpServers": {}}`). The interpolation is interesting for Rigfile: a secret could be referenced by environment variable instead of wrapped in `rigfile exec`, but the value would then sit in the editor's environment, so the exec wrapper stays the recommendation. |
| Workspace-level MCP config | Not mentioned in the page read. | **UNVERIFIED**; not built (user-scope only). |
| Instructions/rules | Not researched: Windsurf's/Devin's rules files (`.windsurfrules`, `.windsurf/rules/`) and global rules location were not fetched. | **UNVERIFIED**; not built. |
| Remote MCP servers | The docs disagree on the field name for a remote server's URL: `serverUrl` in one place, `url` in another. | **CONFLICT, unresolved**; not built. Writing the wrong key would silently configure nothing. |

## Decision

**Built 2026-09-27** (owner decision, after confirming the path above): `internal/adapters/devin`, a copy of the Cursor adapter's shape. What it does:

- **MCP servers**, stdio only: written into `~/.config/devin/mcp_config.json` under `mcpServers`, wrapped through `rigfile exec` exactly like every other adapter (no secret value is ever written to the file).
- **Remote MCP servers**: skipped, with a note, because the field-name conflict above is still unresolved.
- **Instructions, skills, subagents, commands, hooks, permissions**: none are written; each produces a note rather than being silently dropped (working agreement 2: nothing is written on a guess).

Target name: `devin` (`internal/targets`, `internal/targets/capabilities/devin.yaml`, `docs/targets/matrix.md`). Tests: `internal/adapters/devin/devin_test.go` (goldens for macOS/Linux/Windows, a round-trip capture test, a test that user-added servers and secret values never appear in the file).

## What would extend it (owner)

1. **Remote servers**: add one through the app's UI (with a fake `https://` URL) and check whether the resulting JSON key is `serverUrl` or `url`.
2. **Rules/instructions**: find where Devin keeps project or global instructions (if anywhere) and send the location.
3. **Windows path**: run the app on Windows and confirm `%APPDATA%\devin\mcp_config.json`.
