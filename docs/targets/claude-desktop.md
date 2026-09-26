# Target: Claude Desktop

**Date checked:** 2026-09-26. Source: `modelcontextprotocol.io/quickstart/user` ("Connect to local MCP servers"), fetched as markdown.

- **OS support (official):** "Claude Desktop is available for macOS and Windows." **No Linux build** → the adapter reports "Claude Desktop is not available on Linux" there (the plan's "verify official Linux support" is answered: none).
- **Config file:** macOS `~/Library/Application Support/Claude/claude_desktop_config.json`; Windows `%APPDATA%\Claude\claude_desktop_config.json` (the Edit Config button creates it). Changes need a **full restart** of the app.
- **Shape:** `{"mcpServers": {"<name>": {"command": "npx", "args": [...], "env": {...}}}}`. Only local stdio servers live in this file; remote connectors are managed in the app UI ("Manage connectors"), so canonical `transport: http` servers are skipped with "add it in Claude Desktop → Connectors".
- **Windows:** the official example uses plain `npx` with `C:\\Users\\...` paths (no `cmd /c`), and the troubleshooting note says `%APPDATA%\npm` must exist (npm installed globally) and that a server may need `APPDATA` in its `env`. **CONFLICT with plan §9.4** ("configs typically need `cmd /c npx`"): plain `npx` is the documented form for Claude Desktop; the Windows `rigfile exec` wrapper handles `.cmd` shims itself, so the config entry is `rigfile exec … -- npx …` either way.
- **Everything else** (instructions, skills, hooks, permissions): none. The adapter is MCP-only; other items are "not supported by Claude Desktop" notes.
- **Logs:** macOS `~/Library/Logs/Claude`, Windows `%APPDATA%\Claude\logs` (`mcp.log`, `mcp-server-<name>.log`): useful for `doctor`.
- **Secrets caveat:** the file is plain JSON readable by anything running as the user. Rigfile only writes `rigfile exec --secret ENV=ref` wrapper entries, never values.
