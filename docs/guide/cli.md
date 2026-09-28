# CLI reference

Every `rigfile` command, what it is for, and an example. Run `rigfile <command> --help` for all of a command's flags. Commands that talk to a registry take `--registry <url>`, or read `RIGFILE_REGISTRY` from the environment — `https://your-registry.example` below stands for wherever one is running. Sharing through GitHub (`pull`, `publish --to-github`) needs no registry at all.

## Build and apply a rig

### `rigfile init`
Capture your current setup into a new rig. Read-only.
```sh
rigfile init --name you/my-rig                 # Claude Code, into ./rig
rigfile init --from codex --out codex-rig --name you/codex-rig
```

### `rigfile validate`
Check a rig against the schema and the rules a schema cannot express (pinning, portable paths, secret-looking values).
```sh
rigfile validate my-rig
```

### `rigfile plan`
Show exactly what applying would change, per tool. Writes nothing.
```sh
rigfile plan my-rig
rigfile plan my-rig --project ~/code/app      # also project-scope items for that repository
```

### `rigfile apply`
Apply a rig after showing the plan and asking. Everything touched is backed up first.
```sh
rigfile apply my-rig
rigfile apply my-rig --target codex           # configure a tool even if it was not detected
rigfile apply my-rig --sandbox                # also turn on Claude Code's OS sandbox profile
```
Useful flags: `--yes` (don't ask), `--update-lock` (accept a deliberate lock change), `--overwrite` (replace hand-edited managed content), `--no-tools`, `--no-git`, `--models now|later|skip`.

### `rigfile lock`
Write or refresh `rigfile.lock` next to the rig.
```sh
rigfile lock my-rig
```

### `rigfile ui`
The plan and your checklist in a local browser page (on this computer only).
```sh
rigfile ui my-rig
```

## Keep it healthy

### `rigfile doctor`
Health check: secret store, detected tools, drift, base-secure status.
```sh
rigfile doctor
rigfile doctor --fix                  # re-apply the last rig to repair drift (asks first)
rigfile doctor --git ~/code/app       # scan a repository's history for committed secrets
```

### `rigfile diff`
Show drift: what changed on disk since the last apply.
```sh
rigfile diff
```

### `rigfile rollback`
Undo a run (the newest by default).
```sh
rigfile rollback --list
rigfile rollback
```

## Secrets and sign-ins

### `rigfile secrets`
Store and manage secret values. Values are never printed.
```sh
rigfile secrets set alpaca/api_key
rigfile secrets status alpaca/api_key
rigfile secrets list
rigfile secrets rm alpaca/api_key
```

### `rigfile exec`
Run a command with secrets injected into its environment only.
```sh
rigfile exec --secret GITHUB_TOKEN=github/token -- gh api user
```

### `rigfile broker`
The optional secret broker (Level 2): servers get surrogate values; a local proxy swaps in the real key only for approved hosts.
```sh
rigfile broker install && rigfile broker enable
rigfile broker status
rigfile broker exclude my-server      # run one server at Level 1
```

### `rigfile logins`
Walk through the sign-ins the applied rig needs.
```sh
rigfile logins
rigfile logins --provider github
```

## Share and reuse

### `rigfile login`, `rigfile logout`, `rigfile whoami`
Sign in to a registry (device flow: confirm a code in your browser). The token is kept in your keychain.
```sh
rigfile login --registry https://your-registry.example
rigfile whoami --registry https://your-registry.example
```

### `rigfile publish`
Scrub, scan and publish a rig to GitHub or to a registry. With no destination it is a dry run.
```sh
rigfile publish my-rig                                     # dry run
rigfile publish my-rig --to-github your-login               # creates and pushes github.com/your-login/my-rig
rigfile publish my-rig --to-github your-login --public
rigfile publish my-rig --to-registry                        # private
rigfile publish my-rig --to-registry --public
```

### `rigfile pull`
Fetch a rig, show who published it, its trust facts and plan, and apply on approval.
```sh
rigfile pull owner/name --registry https://your-registry.example
rigfile pull github.com/owner/repo@v1.2.0 --plan-only
```

### `rigfile update`
Re-resolve the source of the last pulled rig and show what changed.
```sh
rigfile update --plan-only --diff
```

### `rigfile changes`
Compare two versions of a rig without applying anything.
```sh
rigfile changes owner/name@1.0.0 owner/name@1.1.0 --diff
```

### `rigfile fork`
Start your own rig from someone else's: a copy, or `--extend` to build on it.
```sh
rigfile fork owner/name --name you/my-rig
rigfile fork owner/name --name you/my-rig --extend
```

### `rigfile collection`
Curated lists of rigs on the registry.
```sh
rigfile collection create starter-rigs --title "Starter rigs"
rigfile collection add starter-rigs owner/name --note "why it's good"
rigfile collection show you/starter-rigs
```

### `rigfile org`
Organisations: a namespace several people publish under.
```sh
rigfile org create acme --title "Acme"
rigfile org add acme bob --role member
rigfile org members acme
```

## Beyond one machine

### `rigfile sync`
End-to-end encrypted sync of your own private files between your machines.
```sh
rigfile sync init ~/vault --git
rigfile sync track ~/.claude/CLAUDE.md
rigfile sync push
```

### `rigfile models`
Local models: choose per machine, verified download, service, agents.
```sh
rigfile models list
rigfile models pull
rigfile models url local-coder
```

## The CLI itself

### `rigfile self-update`
Install the latest release after verifying its signature and checksum.
```sh
rigfile self-update --check
```

### `rigfile verify-signature`
Check a minisign signature on a downloaded file.
```sh
rigfile verify-signature downloaded-file.tar.gz        # expects downloaded-file.tar.gz.minisig next to it
```

### `rigfile hook`
The built-in agent hooks (`guard`, `write-guard`, `redact`). Tools call these; you normally don't.
```sh
rigfile hook run guard
```
