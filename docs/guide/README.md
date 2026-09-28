# Rigfile documentation

Everything you need to capture, review, apply and share an AI-tool setup with Rigfile. New here? Start with [Getting started](getting-started.md).

## Start

- [Getting started](getting-started.md) — install the CLI, capture your setup, apply it and share it, in five minutes.
- [Concepts](concepts.md) — rigs, layers, targets, the plan, the lockfile and base-secure.

## Guides

- [Writing a rig](writing-a-rig.md) — a complete, annotated `rigfile.yaml`, section by section.
- [Secrets](secrets.md) — `secret://` references, the keychain, `rigfile exec` and the broker.
- [Publishing and sharing](publishing.md) — publish to git or a registry: scrubbing, pinning, scanning, visibility.
- [Pulling a rig safely](pulling.md) — sources, the plan screen, trust facts, updates and rollback.
- [Forks, collections and organisations](collaboration.md) — build on someone's rig, curate lists, publish as a team.
- [Local models](local-models.md) — run a local model per machine, verified and pinned.
- [Sync between your machines](sync.md) — end-to-end encrypted sync of your own private files.

## Reference

- [Manifest reference](manifest.md) — every field of `rigfile.yaml`.
- [CLI reference](cli.md) — every `rigfile` command, with examples.
- [Supported tools](tools.md) — what Rigfile can configure in each AI tool.
- [Security model and limits](security.md) — what Rigfile protects, how, and what it does not.
- [FAQ and troubleshooting](faq.md) — common questions and the errors people actually hit.

---

This guide is plain Markdown, meant to be read on GitHub. It also ships as a website (`internal/registry/web/docs/`) served by `rigfile-registry`, for anyone who runs one — same content, browsable with a sidebar and search. If you edit one, mirror the change in the other.
