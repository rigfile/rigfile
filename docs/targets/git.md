# Target: git (host module for base-secure)

**Date checked:** 2026-09-25. Official git documentation via git-scm.com and the `git/git` repository (`Documentation/githooks.adoc`, `Documentation/config/core.adoc`). Summarising fetches; behaviour marked **TEST** must be confirmed with real `git` in temp repos (S2-M3/M4 tests do this).

## Hooks

| Fact | Source text (as returned) | Status |
|---|---|---|
| `core.hooksPath` points git at another hooks directory | "By default Git will look for your hooks in the `$GIT_DIR/hooks` directory. Set this to different path, e.g. `/etc/git/hooks`, and Git will try to find your hooks in that directory." | Global. Docs imply **replacement** (repo-local `.git/hooks/*` stop running) but do not say so outright: **TEST**. Rigfile's shims therefore chain to the repo's own hook. |
| Relative `core.hooksPath` | "interpreted as relative to the directory where hooks are executed" | Rigfile writes an **absolute** path. |
| `~` in `core.hooksPath` | Not stated for hooksPath (stated for excludesFile) | **UNVERIFIED**; use absolute. |
| Disable all hooks | "set this to `/dev/null`" | A human or agent can do `git -c core.hooksPath=/dev/null …`: base-secure denies/asks on `git -c core.hooksPath` and `git config core.hooksPath` (§8.2) but a local user can always bypass. |
| `pre-commit` | no args; non-zero aborts the commit; "can be bypassed with the `--no-verify` option" | |
| `commit-msg`, `pre-merge-commit` | also bypassable with `--no-verify` | |
| `prepare-commit-msg` | "not suppressed by the `--no-verify` option" | Not useful for scanning content (runs before the message is final, tree already staged: could still scan the index; **TEST**). |
| `pre-push` | args: remote name, remote URL; stdin lines `<local-ref> <local-oid> <remote-ref> <remote-oid>`; new remote branch → remote oid all zeros; deletion → local ref `(delete)` and zero local oid | |
| **`git push --no-verify`** | "Toggle the pre-push hook … With `--no-verify`, the hook is bypassed completely." | **CONFLICT with plan §8.1c**: pre-push does *not* catch a `--no-verify` commit if the pusher also passes `--no-verify` to push. |
| **`reference-transaction`** | "invoked by any Git command that performs reference updates … preparing, prepared, committed or aborted"; stdin `<old> <new> <ref>`; "In [preparing/prepared] states, a non-zero exit status will cause the transaction to be aborted"; not affected by `--no-verify` | **NEW OPTION:** a `--no-verify`-proof local gate: in `prepared` state, scan the new commit(s) of `refs/heads/*` updates and abort on a finding. Costs: runs for every ref update (fetch, rebase, gc…), so it must be fast and skip quickly (only scan when `<new>` is a commit reachable from local work and the ref is a branch/tag being created or advanced; skip remote-tracking refs). **TEST** performance and semantics in S2-M3 spike. |
| Env for hooks | `GIT_DIR`, `GIT_WORK_TREE` etc. exported | For `pre-commit`, scan the **index** (`git diff --cached`), not the working tree. |

## Config

| Fact | Detail |
|---|---|
| `core.excludesFile` | "pathname to the file that contains patterns … not meant to be tracked, in addition to `.gitignore` and `.git/info/exclude`"; default `$XDG_CONFIG_HOME/git/ignore`, falling back to `$HOME/.config/git/ignore`; `~` expanded. A single value: git has one global excludes file, so a user's existing one is spliced (marked block), not replaced. |
| Global config location | `--global` writes `~/.gitconfig`, or `$XDG_CONFIG_HOME/git/config` if that exists and `~/.gitconfig` does not. |
| Test isolation | `GIT_CONFIG_GLOBAL=<file>` "take the configuration from the given files instead from global or system-level configuration" → all Stage 2 tests run with a temp `HOME`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM=/dev/null`. |
| Reading/restoring previous values | `git config --global --get <key>` (exit 1 if absent), `--show-origin` to see which file; `git config unset <key>` (the `--unset` form is deprecated in current docs; support both, prefer whichever the installed git accepts). `include.path` exists but is not used. |
| Protected by Claude Code | `.gitconfig` and `.config/git` are on Claude Code's protected-path list (see `claude-code.md` §12.4). |

## Not covered here (Stage 3)
Git for Windows' bundled `sh` running hooks, GitHub Desktop / VS Code / JetBrains invocation quirks, and case-insensitive file-system behaviour are research items for Stage 3; the matcher is built case-insensitive and CRLF-safe from the start.

## Empirical results (S2-M3, 2026-09-25, git 2.50.1 Apple Git-155, macOS arm64)

All confirmed with real `git` in temp repos (`internal/githook`, `cmd/rigfile/githooks_e2e_test.go`):

| Claim | Result |
|---|---|
| `core.hooksPath` replaces `.git/hooks` | **Confirmed**: with it set, a repo-local `pre-commit` does not run; unset it and it does (`TestHooksPathSilencesRepoLocalHooks`). The chaining design (S2-M4) is required, not optional. |
| `git commit --no-verify` skips pre-commit | Confirmed. |
| `git push --no-verify` skips pre-push | **Confirmed** (the documented gap): the push goes through. |
| `reference-transaction` (prepared, non-zero exit) blocks `git commit --no-verify` | **Confirmed**: the commit is refused, the branch does not move, git prints the hook's message. Ordinary git (commit, branch, checkout, rebase, merge, tag, annotated tag, fetch, branch -D) keeps working with the backstop installed. |
| Hook invocations per `git commit` | The reference-transaction hook runs **5 times** (preparing/prepared/committed, an aborted transaction, prepared/committed again). Hence the `sh` pre-filter in the shim: only `prepared` on `refs/heads/*` or `refs/tags/*` starts Rigfile. |
| Cost | `git commit`: ~20 ms bare. `rigfile hook pre-commit` adds ~50-75 ms **on this machine, where each git subprocess costs ~12 ms** (Apple's `/usr/bin/git` shim); it spawns 4 (`diff --cached`, `cat-file --batch`, `write-tree`, and the git dir when `GIT_DIR` is not exported). The backstop adds 0-35 ms depending on the shim version measured (noisy): a commit whose tree pre-commit approved needs **zero git subprocesses** (the new commit's tree and parent are read from the loose object file) and only a ~6 ms Go start-up. Linux with a stock git will be much faster; re-measure in the container E2E. |

Design consequences: pre-commit records the approved index tree in `<git-dir>/rigfile-scanned-trees`; the backstop skips commits whose tree is in that set; it fails **open** on internal errors (a bug must not brick every ref update) but blocks on findings; pre-commit and pre-push fail **closed**.
