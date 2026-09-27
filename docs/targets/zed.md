# Target: Zed (S8-M6) — settings path CONFIRMED on macOS 2026-09-27 (owner's machine); BUILT

**Date checked:** 2026-09-26, through a summarising fetcher and search.

| Category | Finding | Status |
|---|---|---|
| MCP servers | `https://zed.dev/docs/ai/mcp`: configured in Zed's `settings.json` under the key **`context_servers`**. Local server: `{"command": "...", "args": [...], "env": {}}`. Remote server: `{"url": "https://...", "headers": {"Authorization": "Bearer ..."}}`. | Verified (the page was read directly and the shape is stated). |
| `settings.json` path | **Confirmed on macOS, 2026-09-27** (owner ran `zed: open settings file` on their own machine): `~/.config/zed/settings.json`, exactly matching the vendor docs' Linux path and resolving the three-way conflict against the other two candidates (`~/Library/Application Support/Zed`, `~/.zed/settings.json`). Windows path (`%APPDATA%\Zed` per the uninstall page) still unconfirmed by a real run. | **Verified on macOS**; Windows still from docs only. |
| Format | `settings.json` accepts comments (JSONC). Rigfile's JSON editor (`internal/jsonedit`) now edits JSON with comments and trailing commas (comments outside the edited member are preserved), so the format is no longer an obstacle. | Verified from the docs' own examples; **UNVERIFIED** that every Zed version tolerates trailing commas. |
| Instructions | `https://zed.dev/docs/ai/rules` returned 404. Zed's rules mechanism (a project `.rules` file, a rules library in the app) was **not** verified. | **UNVERIFIED** |
| Secrets | `context_servers` `env` and `headers` take literal strings; there is no reference syntax in the page read, so the `rigfile exec` wrapper would apply to local servers, and remote servers with a secret header would be skipped with a note (as for Cursor and Gemini). | Design only |

## Decision

**Built 2026-09-27**: `internal/adapters/zed`. What it does:

- **MCP servers**, both stdio and remote (unlike Devin, Zed's docs are unambiguous about the remote shape): written into `settings.json` under `context_servers`, through `internal/jsonedit` (comment-preserving — a real Zed `settings.json` ships with extensive comments by default, and the edit only touches the member it owns, splicing the change onto the original bytes so everything else survives byte-for-byte). Stdio servers wrapped through `rigfile exec`, so no secret value is ever written to the file.
- **Capture** (`rigfile init --from zed`) masks out comments/trailing commas (`jsonedit.Mask`, added for this) before parsing, so it reads a real, commented file correctly rather than failing on it.
- **Instructions, skills, subagents, commands, hooks, permissions**: none written; each produces a note (`https://zed.dev/docs/ai/rules` returned 404 when checked, so there is still no verified rules location).

Registered as target `zed`; tests: goldens for macOS/Linux/Windows, a comment-preservation test, a remote-server test, a round-trip capture test (including one with comments and trailing commas), plus `jsonedit.Mask`'s own tests.

## What would extend it (owner)

Run `zed: open settings file` on Windows and note the path (the `%APPDATA%\Zed\settings.json` guess is from the vendor docs only, never run). Find where Zed keeps project or global instructions, if anywhere, and send the location.
