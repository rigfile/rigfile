# Concepts

## Rig

A rig is a directory with a `rigfile.yaml` at its root and the files that manifest names: instruction Markdown, skill folders, agent and command files, hook scripts. It is plain text, reviewable in a pull request, and portable between machines and operating systems. A rig never contains secret values, only references to them.

Every rig has a name (`owner/name`, lower case) and a version (semantic versioning, `1.2.0`).

## Targets

A **target** is an AI tool Rigfile can configure: Claude Code, Codex CLI, Gemini CLI, Cursor, Claude Desktop, GitHub Copilot in VS Code, Zed and Devin. Each target has an adapter that writes that tool's own format to that tool's own location. Tools support different things; when a rig asks for something a tool cannot take, the plan says so, per item. Nothing is dropped silently. See [Supported tools](/docs/tools).

`targets:` in the manifest narrows which tools a rig is meant for. By default Rigfile configures every tool it detects on the machine (Claude Code is always configured).

## Layers and `from:`

A rig can build on other rigs:

```yaml
from:
  - acme/python-dev@^2
```

Layers are merged in order, earlier first, and your rig on top. Merging is per category and deterministic: lists of items are keyed by id and later layers win; permission rules are combined, and a **deny always wins** over an allow or ask from any layer. The version range (`^2`, `^1.4`) picks the newest matching published version, which is then pinned in the lockfile.

`rigfile/base-secure` is always the first layer, whether you list it or not.

## base-secure

A safety layer applied under every rig: deny rules for credential files, a guard hook for risky shell commands, a hook that checks what the agent writes for secrets, redaction of secrets in tool output, and git secret scanning on commit and push. A rig can add to it; it can never remove or weaken it. How much of it a tool enforces depends on the tool. See [Security model and limits](/docs/security).

## The plan

`rigfile plan` computes exactly what applying would do, per target: files and marked sections written, MCP servers registered, hooks installed, permission rules added, tools installed, and warnings. It writes nothing. `apply`, `pull` and `update` all show the same plan and ask before writing (unless you pass `--yes`).

Rigfile only touches content it owns: instructions go into **marked sections** of files like `CLAUDE.md`, never replacing your own text; JSON and TOML settings are edited structurally, keeping every setting that is not Rigfile's. If you hand-edited something Rigfile manages, the plan shows a conflict and leaves it alone unless you pass `--overwrite`.

## Backups, drift and rollback

Every apply is a **run**: files are backed up before they are written, and the run is recorded.

- `rigfile diff` shows what changed on disk since the last apply (drift).
- `rigfile rollback` undoes the newest run; `rigfile rollback --list` shows all runs.
- `rigfile doctor --fix` re-applies the last applied rig to repair drift (it shows the plan and asks first).

## The lockfile

`rigfile.lock` sits next to `rigfile.yaml` and records exactly what was resolved: each layer's version and hash, each pulled source's commit and tree hash, and signers. Commit it with your rig. A later resolution that does not match the lock (a moved git tag, a different signer, changed content) is refused rather than applied silently. `rigfile lock` writes or refreshes it; `apply --update-lock` accepts a deliberate change.

## Secrets

Manifests hold `secret://path` references; values live in your OS keychain (or an encrypted-file fallback) on each machine. When a tool starts an MCP server, it starts it through `rigfile exec`, which fetches the values and puts them into that one process's environment. See [Secrets](/docs/secrets).

## Registry

This site. It stores published versions (immutable once published), scans every upload, shows trust facts, and serves pulls. Rigs are private until made public. You can also share rigs through any git host; see [Publishing and sharing](/docs/publishing).
