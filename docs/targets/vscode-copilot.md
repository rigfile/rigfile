# Target: GitHub Copilot in VS Code (S8-M6) — NOT BUILT: user-level paths UNVERIFIED

**Date checked:** 2026-09-26, through a summarising fetcher and search.

| Category | Finding | Status |
|---|---|---|
| Workspace MCP config | `https://code.visualstudio.com/docs/copilot/customization/mcp-servers`: **`.vscode/mcp.json`**, top-level **`servers`**; each server `type` (`stdio` or `http`), `command`, `args`, `env`, `url`, `headers`. A portable variant is `.mcp.json` at the project root with top-level `mcpServers`. Secrets: `"${input:variable-id}"` prompts the user at runtime and stores the value securely. | Verified (read twice). It is a **project-scope** file, a different scope model from the user-level files Rigfile's adapters manage. |
| User MCP config | "Run **MCP: Open User Configuration** to open the `mcp.json` in your user profile folder." The path itself is not stated. A second page (`.../agents/reference/mcp-configuration`) mentions `~/.copilot/mcp-config.json` for the *portable* format (Windows form was **inferred by the fetcher**, not stated). | **UNVERIFIED**. Profiles (multiple VS Code profiles each with their own `mcp.json`) add a per-profile dimension. |
| Instructions | `https://code.visualstudio.com/docs/copilot/customization/custom-instructions`: project `.github/copilot-instructions.md`, `AGENTS.md` (setting `chat.useAgentsMdFile`), `.github/instructions/**/*.instructions.md` (frontmatter `name`, `description`, `applyTo`), and `CLAUDE.md` / `.claude/rules`. User level: `~/.copilot/copilot-instructions.md` and `~/.copilot/instructions/**/*.instructions.md` (for "Copilot agent host sessions"). | Project-level: verified. User-level: the page ties it to one session type; **UNVERIFIED** for the ordinary VS Code chat. |
| Input variables vs `rigfile exec` | `${input:...}` keeps a secret out of the file and out of git, like `secret://` refs do, but the value lives in VS Code's store and is handed to the server's environment: Level 1 at best. Wrapping with `rigfile exec` (and, later, Level 2) is stronger. | Design |

## Decision

No adapter. A useful one needs a scope decision (project files vs the user profile) and the profile path; both are the owner's call, and the user path is unverified.

## What settles it (owner)

1. Say whether Rigfile should write **project** files for this target (`rigfile apply --project <dir>` writing `.vscode/mcp.json` and `.github/copilot-instructions.md`), which is fully verified and needs no user-path knowledge, or the **user profile**.
2. For the user profile: run **MCP: Open User Configuration** on each OS and note the path (including a non-default profile).

Project-scope is the recommendation: it is verified, portable across OSes, reviewable in a pull request, and does not touch the person's editor profile. It needs `scope: project` support for MCP servers in the manifest (today `scope: project` exists for instructions only), which is a schema change to discuss first.
