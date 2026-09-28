# Pulling a rig safely

A rig can install hooks and scripts and register MCP servers, and those run on your machine with your permissions. Rigfile's job when you pull is to show you exactly what a rig will do and who it comes from, and to change nothing until you say yes.

## Sources

```sh
rigfile pull github.com/owner/repo                      # a git repository (default branch)
rigfile pull github.com/owner/repo@v1.2.0               # a tag, branch or 40-hex commit
rigfile pull github.com/owner/repo@main//rigs/python    # a rig in a subdirectory
rigfile pull gitlab.com/owner/repo@v1.0.0
rigfile pull https://git.example.com/team/rigs.git@v2   # any git URL (https or ssh; needs git)

rigfile pull owner/name --registry https://your-registry.example          # from the registry: newest published version
rigfile pull owner/name@1.2.0 --registry https://your-registry.example    # an exact version
```

A git reference is resolved to a commit once, and the downloaded tree is hashed. A branch or default branch is pinned to that commit in your lockfile with a warning; a tag that later points somewhere else is reported as "the tag moved" and never applied silently.

Nothing from the rig runs while it is fetched: no install scripts, no "prepare" step.

## What the plan screen shows

Before the usual plan, a pull prints:

- **Source**: where the rig came from, and for git sources the commit and tree hash.
- A **banner** when the rig is not yours: "you did not write it. Review every hook, script and MCP command below."
- **Trust facts**, as numbers rather than a score: the rig's and the version's age, how many versions and stars it has, when the publisher was first seen and how many public rigs they have, a verified badge if the registry verified them, how many versions were yanked, and whether moderators ever removed anything of this publisher's.
- **Signature**: signed by the publisher's own identity, signed by someone else, or not signed.
- **Similar names**: a warning when the name is close to a popular or verified rig (typosquatting).
- **Static analysis**: heuristic findings over the rig's scripts, hooks, MCP definitions and instructions (for example piping a download to a shell, reading credential files, or instructions that try to override safety rules). Levels are notice, caution and danger. It never quotes the code, and it can miss things.
- The **plan** itself, with every hook marked "executes code".

`--plan-only` stops here. `--yes` skips the question but not the screen, and it refuses a rig with danger-level findings unless you also pass `--accept-danger`. `--require-signature` refuses unsigned rigs, or rigs signed by someone other than the publisher.

## Staying up to date

```sh
rigfile update --plan-only     # re-resolve the last pulled rig and show what would change
rigfile update                 # the same, then apply on approval
rigfile update --diff          # also show the text of changed files
```

An update whose signer differs from the one you pulled before is refused unless you pass `--accept-signer-change`.

To compare any two versions without applying anything:

```sh
rigfile changes owner/name@1.0.0 owner/name@1.1.0 --registry https://your-registry.example --diff
```

The same comparison is on every rig page ("changes" next to each version). It flags what deserves a look: added or changed MCP servers and hooks, new hosts a server may reach, secrets newly read or sent to different hosts, new `allow` rules or removed protections, new packages, changed base rigs, and changed instructions, skills and scripts.

## Undo

```sh
rigfile rollback --list        # every run, newest first
rigfile rollback               # undo the newest run
rigfile rollback <run-id>      # undo a specific run
```

Rollback restores the backups taken before the run. If you edited a file after Rigfile wrote it, rollback stops and tells you, unless you pass `--force`.

## Private rigs

A private rig is visible only to its owner (or its organisation's members). To everyone else, a private rig and a rig that does not exist look the same: "not found", with no hint that something is there.

## Reporting

If a rig you pulled from a registry looks malicious, leaks a secret, or impersonates someone, use **Report this rig** on its page (or the `/report` form) — that registry's administrators review reports; its `/legal/takedown` page describes what happens next. For a git-shared rig, open an issue against the repository it came from, or its host's own abuse-reporting flow.
