# ADR 0002: How Rigfile writes user-scope MCP servers for Claude Code

- **Status:** Accepted 2026-09-25 (owner)
- **Context:** Claude Code stores user- and local-scope MCP servers in `~/.claude.json`, a file it also uses for its own state (sign-in session, per-project trust decisions). Editing it while Claude Code runs risks lost updates (`docs/targets/claude-code.md` §5). The vendor documents `claude mcp add-json <name> '<json>' --scope user`, `claude mcp get <name>` and `claude mcp remove <name>` (https://code.claude.com/docs/en/mcp, checked 2026-09-25).
- **Decision:** Rigfile writes user-scope MCP servers by calling the `claude` CLI (`add-json --scope user`), never by editing `~/.claude.json` directly. Project-scoped rigs write `<project>/.mcp.json` themselves (a plain, team-owned file).
- **Consequences**
  - Requires the `claude` CLI on `PATH`; if absent the plan marks MCP servers "skipped: claude not installed" (never a silent drop).
  - The output formats of `claude mcp get/list` are **not documented**, so Rigfile does not parse them for content. It uses exit status for presence and tracks what it installed (name + hash of the JSON it added) in `state.json`, giving idempotence and drift detection without reading `~/.claude.json`.
  - A server with the same name that Rigfile did not install is a **conflict** (default: keep the user's, skip ours), consistent with merge-semantics §4.3.
  - Secrets never appear in the JSON: the entry is `command: rigfile`, `args: [exec, --secret ENV=ref, --, <real command>]` (plan §7.2 Level 1).
  - The exact exit codes for "not found" and the `--scope` flag on `remove` are UNVERIFIED; the implementation isolates them behind one interface and is tested against a fake `claude`. A real-CLI smoke test is a Stage 1 manual gate.
- **Alternatives rejected:** direct JSON edit of `~/.claude.json` (lost-update risk, coupling to an undocumented layout); project-only (does not cover a personal setup).
