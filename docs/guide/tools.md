# Supported tools

Rigfile writes each AI tool's own configuration format to that tool's own location. Tools differ in what they can take, so support differs per category.

When a rig contains something a tool cannot take, the plan says so for that item and that tool. Nothing is dropped silently.

**base-secure** is enforced (as permission rules and hooks) only where the tool has a documented permission and hook contract. Everywhere else it is written as instructions the agent is asked to follow, which is weaker, and the plan says that too.

Where support is marked **UNVERIFIED**, the vendor's documentation did not settle it when checked, so Rigfile does not write it.

## The matrix

[**docs/targets/matrix.md**](../targets/matrix.md) — every tool, every category, generated straight from `internal/targets/capabilities/*.yaml` by `go test ./internal/targets -update`, so it can never drift from what `rigfile plan` actually does (a test fails the build if anyone forgets to regenerate it).

## Tool by tool

Deeper notes per tool, including where each one reads its configuration and what was verified live vs. from vendor docs only:

- [Claude Code](../targets/claude-code.md)
- [Claude Desktop](../targets/claude-desktop.md)
- [Codex CLI](../targets/codex.md)
- [Cursor](../targets/cursor.md)
- [Devin](../targets/devin.md)
- [Gemini CLI](../targets/gemini-cli.md)
- [GitHub Copilot in VS Code](../targets/vscode-copilot.md)
- [Zed](../targets/zed.md)
