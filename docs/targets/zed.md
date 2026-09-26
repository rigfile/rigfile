# Target: Zed (S8-M6) — NOT BUILT: settings path UNVERIFIED

**Date checked:** 2026-09-26, through a summarising fetcher and search.

| Category | Finding | Status |
|---|---|---|
| MCP servers | `https://zed.dev/docs/ai/mcp`: configured in Zed's `settings.json` under the key **`context_servers`**. Local server: `{"command": "...", "args": [...], "env": {}}`. Remote server: `{"url": "https://...", "headers": {"Authorization": "Bearer ..."}}`. | Verified (the page was read directly and the shape is stated). |
| `settings.json` path | The pages read do **not** state the file's path. `https://zed.dev/docs/uninstall` lists Zed's directories: macOS `~/Library/Application Support/Zed` **and** `~/.config/zed`; Linux `~/.config/zed`; Windows `%APPDATA%\Zed` and `%LOCALAPPDATA%\Zed`. A search result says `debug.json` lives in `~/Library/Application Support/Zed` on macOS, `$XDG_CONFIG_HOME/zed` (default `~/.config/zed`) on Linux, `%APPDATA%\Zed` on Windows, and another says settings are in `~/.zed/settings.json` on macOS. | **UNVERIFIED**, and the sources disagree for macOS (three candidate locations). |
| Format | `settings.json` accepts comments (JSONC). Rigfile's JSON editor (`internal/jsonedit`) preserves comments in the files it edits, so this is workable. | Verified from the docs' own examples; **UNVERIFIED** that every Zed version tolerates trailing commas. |
| Instructions | `https://zed.dev/docs/ai/rules` returned 404. Zed's rules mechanism (a project `.rules` file, a rules library in the app) was **not** verified. | **UNVERIFIED** |
| Secrets | `context_servers` `env` and `headers` take literal strings; there is no reference syntax in the page read, so the `rigfile exec` wrapper would apply to local servers, and remote servers with a secret header would be skipped with a note (as for Cursor and Gemini). | Design only |

## Decision

No adapter yet: the one file the adapter would write has no verified path on macOS.

## What settles it (owner)

Run `zed: open settings file` on macOS, Linux and Windows and note the path shown in the title bar or with `Cmd/Ctrl-Shift-P` "copy path". With that, the adapter is: a `context_servers` member set in that file through `jsonedit` (comment-preserving), `rigfile exec` wrapping for stdio servers, remote servers without secrets written as `url`/`headers`, instructions skipped with a note until the rules format is verified.
