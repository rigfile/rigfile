# jia-rig (fixture)

The owner's Claude Code setup, captured with `rigfile init` and reviewed by the owner (2026-09-25).

- Secret values were never captured; `alpaca/*` are `secret://` references only.
- The `snowflake` MCP server is left out on purpose (it referenced a machine-specific path).
- `alpaca` is intentionally **unpinned**: `rigfile validate` reports a warning, not an error, so the
  fixture exercises the "unpinned MCP package" path.
- Two symlinked skills and the `synced` folder were skipped by `init`.

Used by `TestJiaRigFixture` in `cmd/rigfile/main_test.go`.
