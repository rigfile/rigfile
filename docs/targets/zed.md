# Target: Zed (S8-M6) — settings path CONFIRMED on macOS 2026-09-27 (owner's machine); NOT BUILT

**Date checked:** 2026-09-26, through a summarising fetcher and search.

| Category | Finding | Status |
|---|---|---|
| MCP servers | `https://zed.dev/docs/ai/mcp`: configured in Zed's `settings.json` under the key **`context_servers`**. Local server: `{"command": "...", "args": [...], "env": {}}`. Remote server: `{"url": "https://...", "headers": {"Authorization": "Bearer ..."}}`. | Verified (the page was read directly and the shape is stated). |
| `settings.json` path | **Confirmed on macOS, 2026-09-27** (owner ran `zed: open settings file` on their own machine): `~/.config/zed/settings.json`, exactly matching the vendor docs' Linux path and resolving the three-way conflict against the other two candidates (`~/Library/Application Support/Zed`, `~/.zed/settings.json`). Windows path (`%APPDATA%\Zed` per the uninstall page) still unconfirmed by a real run. | **Verified on macOS**; Windows still from docs only. |
| Format | `settings.json` accepts comments (JSONC). Rigfile's JSON editor (`internal/jsonedit`) now edits JSON with comments and trailing commas (comments outside the edited member are preserved), so the format is no longer an obstacle. | Verified from the docs' own examples; **UNVERIFIED** that every Zed version tolerates trailing commas. |
| Instructions | `https://zed.dev/docs/ai/rules` returned 404. Zed's rules mechanism (a project `.rules` file, a rules library in the app) was **not** verified. | **UNVERIFIED** |
| Secrets | `context_servers` `env` and `headers` take literal strings; there is no reference syntax in the page read, so the `rigfile exec` wrapper would apply to local servers, and remote servers with a secret header would be skipped with a note (as for Cursor and Gemini). | Design only |

## Decision

No adapter yet, though the macOS path is now confirmed. Still open: the Windows path (run `zed: open settings file` there and note the path), and the rules/instructions format.

## What settles the rest (owner)

Run `zed: open settings file` on Windows and note the path. With the paths known, the adapter is: a `context_servers` member set in that file through `jsonedit` (comment-preserving), `rigfile exec` wrapping for stdio servers, remote servers without secrets written as `url`/`headers`, instructions skipped with a note until the rules format is verified.
