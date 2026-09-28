# Security model and limits

Rigfile moves configuration that can **run code** (hooks, MCP servers, scripts) between people and machines. This page says what protects you, and, just as plainly, what does not.

## Principles

- **Nothing runs unreviewed.** `plan`, `apply`, `pull` and `update` show every change and every piece of code before writing, and change nothing until you approve.
- **Secrets never travel.** Rigs contain references; values stay in each machine's keychain and reach only the process that needs them.
- **A safety floor under every rig.** `rigfile/base-secure` is always applied, and no rig can remove or weaken it.
- **Pinned, hashed, immutable.** Packages are pinned to exact versions, git sources to commits, published versions never change, and the lockfile refuses anything that does not match.
- **Everything is undoable.** Backups before every write; `rigfile rollback` restores them.

## What base-secure puts on your machine

| Layer | What it does |
|---|---|
| Deny rules | The agent may not read `.env` files, private keys (`*.pem`, `*.key`, `id_*` ...), cloud credentials (`~/.ssh`, `~/.aws`, `~/.config/gcloud`, `~/.azure`, `~/.kube`, `~/.gnupg`), Terraform state, token files, or its own login files; may not run `git ... --no-verify`, force-push, or dump the environment. |
| Ask rules | It must ask before `git push`, `rm -rf`, `sudo`, recursive `chmod 777`, publishing packages, privileged containers, piping a download into a shell, and editing its own settings. |
| Guard hook | Unwraps `sh -c`, `eval`, `env`, `sudo`, `xargs`, path-qualified programs and `git -C` before applying the rules above, so simple wrappers do not slip past them. |
| Write guard | Scans what the agent writes (Write/Edit) for secrets, and refuses. |
| Redaction | Replaces secret-looking text in tool output with `[REDACTED:<rule>]` before the model reads it. |
| Git protection | Pre-commit and pre-push secret scanning in your repositories (your existing hooks are chained, not replaced), and credential patterns in your global gitignore. |
| Instructions | A short "security baseline" section in the agent's instructions. |

Optional: `apply --sandbox` also turns on Claude Code's OS-level sandbox with credential denies (macOS, Linux, WSL2), the only layer that stops a *script* from opening a credential file itself.

How much of this each tool enforces is on [Supported tools](/docs/tools): fully in Claude Code, partly in Codex, and as instructions only in tools without a permission and hook contract.

## Known limits

- **Rules match command text.** A tokenizer is not a shell. Known evasions include scripts in another language that run the command, variable indirection, encoded payloads piped to a shell, aliases, and a script that opens a credential file itself. The sandbox is the OS-level answer.
- **Hooks can be turned off** by you (or overridden by managed settings). base-secure guards against agent mistakes and misbehaviour, not against a determined local user.
- `git push --no-verify` skips the pre-push scan (commits are still scanned when they are made).
- **Static analysis is a heuristic.** It makes the obvious visible and raises the cost of the rest; a determined attacker can evade it.
- **The broker** does not cover programs that ignore the system proxy or trust store (Go on macOS/Windows, Java, pinned clients), HTTP/2 to the server, WebSockets, or processes not started through `rigfile exec`.
- **Star counts can be bought**, and similar-name detection is a heuristic.
- **Organisation members** are fully trusted to publish under the organisation's name.

## The registry

- Every upload is unpacked with a hardened extractor, validated, secret-scanned and statically analysed before it is published. Rejected archives are deleted at once; the reason stays visible to the uploader.
- Private rigs are indistinguishable from missing ones to everyone else.
- Pages run no scripts at all, and README and file contents are sanitised and shown as text; a strict content security policy backs this up.
- Sign-in is through GitHub; the registry requests no GitHub permissions. A GitHub login vacated by a rename cannot be taken over to publish under the old owner's rigs.
- Reports go to the administrators; see the [takedown policy](/legal/takedown).

## Reporting a vulnerability

Please do not open a public issue for a security problem. Email **bytebuilderslab@gmail.com** (we aim to acknowledge within 3 business days and give our assessment within 10 days); the project's [security policy](https://github.com/rigfile/rigfile/blob/main/SECURITY.md) has the details. Malicious rigs are not vulnerabilities in Rigfile: report those with **Report this rig** on the rig's page.
