# End-to-end tests

`e2e/run.sh [ubuntu|fedora|alpine|debian|arch ...]` (needs Docker and Go): cross-compiles a static Linux
`rigfile`, builds a fresh image per distro (Ubuntu 24.04, Fedora 41, Alpine 3.20, Debian 12 "bookworm", Arch —
apt, dnf, none, apt, and pacman respectively), and as an **unprivileged user with an empty home** runs
`e2e/scenario.sh`: validate → plan (must write nothing) → apply → idempotent re-apply → secrets set → exec →
doctor (must be green) → drift → rollback → base-secure and git protections → Codex/Gemini CLI/Cursor/Devin/Zed
together → capture round-trip → publish/pull.

Alpine has no `apk` entries in `catalog/tools.yaml` at all — it is here to prove Rigfile degrades honestly
("no supported package manager found") rather than crashing or misbehaving, not to claim verified apk
package support (that needs real package names researched first, per working agreement 2). It runs on musl
libc under busybox `sh`, natively on arm64 (an official image exists), so it is in the default set.

Debian is the closest safe proxy for Raspberry Pi OS (Debian-based) without real Pi hardware. It shares the
`apt` manager with Ubuntu, but `catalog/tools.yaml` already records real, distro-specific differences (e.g.
`gh`'s packaged version, `uv`'s unavailability outside sid/forky) — this distro was never run through the
scenario until 2026-09-27.

`arch` needs an explicit `e2e/run.sh ubuntu fedora alpine debian arch` (CI does this, since
`ubuntu-latest` is a native amd64 runner): archlinux has no official arm64 image, so on an Apple Silicon host it
only runs under QEMU's amd64 emulation, which is **unreliable for this Go binary** — found live, 2026-09-27:
2 of 5 runs crashed with a segfault inside `regexp/syntax`, at a *different* line each time, while the identical
binary ran clean, repeatedly, natively on arm64 (and a minimal `regexp.MustCompile` reproduction of the same
pattern never crashed under the same emulation). A QEMU binary-translation bug, not a Rigfile one — but real
enough that `arch` stays out of the default set on such a host until a native arm64 image exists or a more
reliable emulator (e.g. Docker Desktop's Rosetta-based x86_64 mode, not confirmed available here) is used.

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
