# Target: Windsurf (S8-M6) — NOT BUILT: CONFLICT in the vendor's own documentation

**Date checked:** 2026-09-26, through a summarising fetcher and search (so quotes are second-hand).

| Category | Finding | Status |
|---|---|---|
| MCP config path | `https://docs.windsurf.com/plugins/cascade/mcp` (found by search) gives `~/.codeium/windsurf/mcp_config.json`. The older URL `https://docs.windsurf.com/windsurf/cascade/mcp` now **redirects (307) to `https://docs.devin.ai/desktop/cascade/mcp`**, whose page gives **`~/.config/devin/mcp_config.json`** (or `$XDG_CONFIG_HOME/devin/...`) on macOS and Linux and `%APPDATA%\devin\mcp_config.json` on Windows. A search result also mentions "Devin Desktop profile separation" and "legacy Windsurf Cascade". | **CONFLICT**: the product appears to be moving from Windsurf to a Devin-branded desktop app, and the two documents disagree on the file. Writing to the wrong one would silently configure nothing (or a different product). |
| MCP JSON shape | Top-level `mcpServers`; stdio `command`, `args`, `env`; remote `serverUrl` or `url`, `headers`. Interpolation in `command`, `args`, `env`, `serverUrl`, `url`, `headers`: `${env:VAR_NAME}` and `${file:/path}` (trimmed file contents). | Consistent between the pages read. The interpolation is interesting for Rigfile: a secret could be referenced by environment variable instead of wrapped in `rigfile exec`, but the value would then sit in the editor's environment, so the exec wrapper stays the recommendation. |
| Workspace-level MCP config | Not mentioned in the page read. | **UNVERIFIED** |
| Instructions/rules | Not researched: Windsurf's rules files (`.windsurfrules`, `.windsurf/rules/`) and global rules location were not fetched. | **UNVERIFIED** |

## Decision

No adapter. Shipping one on a guess would violate working agreement 2 and could write a config file the app never reads.

## What settles it (owner, 5 minutes)

On a machine with the app installed: open its MCP settings, use "View raw config" (or the equivalent), and note the file's real path on macOS, Linux and Windows; check whether the app you have is called Windsurf or Devin. Send me the path and the product name; the adapter is then a copy of the Cursor one (`internal/adapters/cursor`): a JSON `mcpServers` member set, `rigfile exec` wrapping, and instructions skipped or mapped once the rules location is known. Add the target name to `internal/targets`, a section to `docs/targets/matrix.md`, and goldens.
