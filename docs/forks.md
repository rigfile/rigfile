# Forks and "use as base" (S8-M2)

A rig can start from another rig in two ways, and the registry shows both without any new write path: the relationship is *derived* from the manifest's `from:` list.

| You want | Do | What it does |
|---|---|---|
| Your own copy to change | `rigfile fork <owner/name[@version]> --name you/your-rig` | Copies the rig (never `.git` or `rigfile.lock`, keeps script permissions), renames it, resets the version to `0.1.0`, credits the original in a `# Forked from` comment, and checks the result validates. The copy is independent: later changes to the original do not reach it (use `rigfile changes` to see them) |
| To stay on top of someone's rig | `rigfile fork <source> --name you/your-rig --extend`, or add a line to `from:` by hand | Writes a small rig whose `from:` names the original (`owner/name@^X.Y` from the version you pointed at). Layers are resolved at apply time, so the original's updates flow in, subject to `rigfile.lock` and the version range |

`<source>` may be a registry rig, a git source, or (for a copy) a local rig directory. A fork that would not validate leaves nothing behind. `--extend` refuses a local directory because a layer has to be something other machines can fetch.

## On the registry

- The rig page has a **Use as a base** box with the `from:` snippet for the version shown, and a **Built on this rig** list.
- `GET /v1/rigs/{owner}/{name}/derived` returns `{total, rigs:[{owner,name,description,stars,latest}]}`; the rig JSON carries `derived` (the count).
- **What counts:** a *public* rig, not removed, whose *newest published* version lists `owner/name` in `from:` (with or without `@range`). A version that dropped the entry stops counting. Private rigs, unpublished versions and the rig itself never appear, so the list reveals nothing `search` would not.
- The rig must itself be visible to the viewer: a private rig's `derived` endpoint is an identical 404.

Tests: `TestForkCopiesARigAsYourOwn`, `TestForkExtendBuildsOnARegistryRig` (`cmd/rigfile`); `TestDerivedRigsAndUseAsBase` (`internal/registry`).
