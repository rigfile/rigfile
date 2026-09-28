# sample-rig (fixture)

A made-up rig in the shape of a real `rigfile init` capture: a skill with a helper script, a command,
an MCP server that reads two secrets, and allow/deny permission lists. No real person's setup.

- Secret values are never part of a rig; `weather/*` are `secret://` references only.
- `weather` is intentionally **unpinned**: `rigfile validate` reports a warning, not an error, so the
  fixture exercises the "unpinned MCP package" path.

Used by `TestSampleRigFixture` in `cmd/rigfile/main_test.go`.
