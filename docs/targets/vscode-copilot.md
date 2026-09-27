# Target: GitHub Copilot in VS Code (S8-M6) — BUILT, PROJECT SCOPE ONLY (user-level paths UNVERIFIED)

**Date checked:** 2026-09-26, through a summarising fetcher and search.

| Category | Finding | Status |
|---|---|---|
| Workspace MCP config | `https://code.visualstudio.com/docs/copilot/customization/mcp-servers`: **`.vscode/mcp.json`**, top-level **`servers`**; each server `type` (`stdio` or `http`), `command`, `args`, `env`, `url`, `headers`. A portable variant is `.mcp.json` at the project root with top-level `mcpServers`. Secrets: `"${input:variable-id}"` prompts the user at runtime and stores the value securely. | Verified (read twice). It is a **project-scope** file, a different scope model from the user-level files Rigfile's adapters manage. |
| User MCP config | "Run **MCP: Open User Configuration** to open the `mcp.json` in your user profile folder." The path itself is not stated. A second page (`.../agents/reference/mcp-configuration`) mentions `~/.copilot/mcp-config.json` for the *portable* format (Windows form was **inferred by the fetcher**, not stated). | **UNVERIFIED**. Profiles (multiple VS Code profiles each with their own `mcp.json`) add a per-profile dimension. |
| Instructions | `https://code.visualstudio.com/docs/copilot/customization/custom-instructions`: project `.github/copilot-instructions.md`, `AGENTS.md` (setting `chat.useAgentsMdFile`), `.github/instructions/**/*.instructions.md` (frontmatter `name`, `description`, `applyTo`), and `CLAUDE.md` / `.claude/rules`. User level: `~/.copilot/copilot-instructions.md` and `~/.copilot/instructions/**/*.instructions.md` (for "Copilot agent host sessions"). | Project-level: verified. User-level: the page ties it to one session type; **UNVERIFIED** for the ordinary VS Code chat. |
| Input variables vs `rigfile exec` | `${input:...}` keeps a secret out of the file and out of git, like `secret://` refs do, but the value lives in VS Code's store and is handed to the server's environment: Level 1 at best. Wrapping with `rigfile exec` (and, later, Level 2) is stronger. | Design |

## Decision (as built)

The adapter (`internal/adapters/vscodecopilot`) is **project-scoped**, which is what was verified: `rigfile apply <rig> --project <dir> [--target vscode-copilot]` writes

- MCP servers into `<project>/.vscode/mcp.json` under `servers` (`type: stdio` through `rigfile exec`, or `type: http` with `url`/`headers`; remote servers whose headers reference secrets are skipped with a note; `${input:...}` was not used because the value would live in VS Code's store, not Rigfile's);
- **project-scope** instructions into a marked section of `<project>/.github/copilot-instructions.md`.

Rules that follow from "these files are committed": no secret value ever (only `secret://` refs turned into `rigfile exec --secret` arguments); **user-scope instructions are never written** (a note says why and how to mark one `scope: project`); nothing is written without `--project`, and nothing outside it; a plan note reminds that teammates need `rigfile` installed and that `targets: [claude-code]` on an item keeps a personal server out. The target is auto-selected only when a project is given and it has a `.vscode` directory (or `code` is on PATH); it never touches the user profile.

`mcp.json` may contain comments in VS Code, so `internal/jsonedit` now edits **JSON with comments and trailing commas**: it masks them (same byte offsets), runs the edit on the mask, and applies the resulting change to the original bytes, so comments outside the edited member survive (a comment on the same line as the member just before an insertion may end up after the inserted member). A file that is not JSON at all is left untouched with a note. This also lifts one obstacle to a Zed adapter (its `settings.json` is JSONC); the other, the settings path on macOS, is still unverified.

Skills, subagents, commands, hooks and permissions have no verified equivalent and are reported, not written. No manifest schema change was needed.

Still to verify (owner): the user-profile `mcp.json` path per OS and profile, if a user-level mode is ever wanted; that VS Code loads the generated `.vscode/mcp.json` entries (`rigfile exec` from PATH) on each OS; that Copilot reads the marked section of `copilot-instructions.md`.
