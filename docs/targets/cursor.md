# Target: Cursor

**Date checked:** 2026-09-26. Sources: `cursor.com/docs/context/rules`, `cursor.com/docs/context/mcp` (the `docs.cursor.com` URLs now redirect to `cursor.com/docs`), via a summarising fetcher. Nothing was read from a real `~/.cursor`.

| Category | Verified facts | Adapter decision |
|---|---|---|
| Instructions | **Project rules**: `.cursor/rules/*.mdc` (Markdown + frontmatter `description`, `globs`, `alwaysApply`); `AGENTS.md` in the project root is a supported alternative. **User Rules are a UI-only setting** ("Customize → Rules"): there is **no user-level rules file**. Team Rules come from the dashboard. | Global instructions **cannot be written to a file**: user-scope `instructions:` are reported as "Cursor: user rules live in the app UI; copy this text into Customize → Rules" and printed (nothing written). Project-scope instructions go to `.cursor/rules/<id>.mdc` with `alwaysApply: true` when `--project` is given. |
| MCP servers | Global `~/.cursor/mcp.json`, project `.cursor/mcp.json`. stdio: `type: "stdio"`, `command`, `args`, `env`, `envFile`; remote: `url`, `headers`, `auth` (static OAuth `CLIENT_ID`, `CLIENT_SECRET`, `scopes`). Interpolation in command/args/env/url/headers: `${env:NAME}`, `${userHome}`, `${workspaceFolder}`, `${workspaceFolderBasename}`, `${pathSeparator}` / `${/}`. The docs do not mention a Windows `cmd /c npx` requirement or the Windows path (by convention `%USERPROFILE%\.cursor\mcp.json`, **UNVERIFIED**). | JSON editor on `~/.cursor/mcp.json`; stdio entries use the `rigfile exec` wrapper (secrets never in the file). |
| Skills, subagents, hooks, commands, permissions | Not covered by the pages read (Cursor has custom commands and hooks in current builds: **UNVERIFIED**). | Skipped with a note; base-secure is "instructions only" for Cursor except MCP hygiene. |
| Credentials | Not documented on the pages read. | — |
| OS support | Desktop app for macOS, Linux, Windows (**UNVERIFIED** on the pages read). | Treated as available on all three; detection = config dir or app present. |
