# End-to-end tests

`e2e/run.sh [ubuntu|fedora ...]` (needs Docker and Go): cross-compiles a static Linux `rigfile`, builds a
fresh image per distro (Ubuntu 24.04, Fedora 41), and as an **unprivileged user with an empty home**
runs `e2e/scenario.sh`: validate → plan (must write nothing) → apply → idempotent re-apply → secrets set
→ exec → doctor (must be green) → drift → rollback.

Stand-ins, on purpose:

* `e2e/fake-claude` replaces the `claude` CLI (only `mcp add-json|get|remove`). The real CLI's output
  formats are undocumented (ADR 0002), so the fake only proves Rigfile's side of the contract.
* No OS keychain exists in the containers, so the encrypted-file secret backend is exercised (via
  `RIGFILE_PASSPHRASE_FILE`). `doctor` prints a warning for that; it is not an error.
* Tools: `sudo apt-get`/`dnf` steps are printed, never run (no root, by design).

The rig is `e2e/rig/` (fake values only).

## macOS (owner-run, not automated)

Docker cannot run macOS. To verify a clean Mac, use a throwaway VM with [Tart](https://tart.run):

```sh
brew install cirruslabs/cli/tart
tart clone ghcr.io/cirruslabs/macos-sequoia-base:latest rigfile-e2e   # admin/admin
tart run rigfile-e2e &                                                # then, inside the VM:
#   brew install go git          (Xcode CLT prompt: accept)
#   git clone <repo> && cd rigfile && git checkout stage-1
#   go build -o /usr/local/bin/rigfile ./cmd/rigfile
#   rigfile validate e2e/rig && rigfile plan e2e/rig
#   rigfile apply e2e/rig             # review the screen, approve
#   rigfile secrets set demo/api_key  # Keychain prompt: this is the check the containers cannot do
#   rigfile doctor                    # expect: secret store = keychain, no warnings
#   rigfile rollback
tart delete rigfile-e2e
```

Things only this run can confirm: the real macOS Keychain path, and (if the real `claude` CLI is
installed in the VM) `claude mcp add-json/get/remove --scope user` behaviour (ADR 0002 open items).
