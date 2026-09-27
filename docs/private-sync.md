# Private sync between your own machines: design (S8, deferred item)

Status: **BUILT (2026-09-26, branch `stage-8c`)**, using the recommendations below for every decision (§5, "as decided"). RIGFILE_PLAN.md §6 gives a manifest key `private:` ("never published; synced only to owner's machines") and §12 lists "private memory sync between the owner's own machines (end-to-end encrypted)" under Stage 8. Sections 1-4 are the design; **§7 is what was built and what differs**.

## 1. Goal and non-goals

**Goal:** the files that make an agent *yours* (your personal `CLAUDE.md`, memory notes, private skills) follow you to your other machines without ever being readable by anyone else, including whoever hosts the storage.

**Non-goals:** syncing secrets (rule 7.3.7: secrets are the OS keychain's job; only refs travel), syncing arbitrary dotfiles, sharing with other people (that is what a rig or an organisation is for), a real-time collaborative editor.

## 2. Threat model

| Threat | Requirement |
|---|---|
| The storage provider reads the files (a git host, a cloud folder, a Rigfile server) | They see ciphertext, file counts and sizes, and the times of changes; never names or contents (names are inside the encrypted index) |
| The provider, or someone who steals its database, changes, deletes or rolls back files | Detected: every file and the index are authenticated; a rollback to an older index is refused |
| A stolen or lost device | Revocable: removing it from the vault stops it reading anything written afterwards. It keeps what it had already decrypted (unavoidable; say so) |
| A new device is enrolled by an attacker | Enrollment needs an already-enrolled device's explicit approval and an out-of-band fingerprint check |
| Secrets end up in a synced file by accident | The scanner runs on everything before it is encrypted; a finding blocks the file unless the person overrides for that file |
| A malicious rig you pulled tries to read or overwrite your private files | Private paths are never taken from a pulled rig's manifest (only from the top-level rig you own) and syncing always goes through the journaled writer (backup, rollback) |

## 3. Recommended design

**Transport is not ours.** Rigfile encrypts; the bytes live somewhere you already trust to *store* (not to *read*): a private git repository you own, or any synced folder (iCloud Drive, Dropbox, Syncthing). Rigfile's own registry does not store them. Reasons: rule 7.3.1 (the server never holds user data it does not need), no new service to run and defend, no abuse or retention duty, and it works offline. The cost is that you supply the place. (Option B below.)

**Keys.** Each device generates an age X25519 identity; the private half goes to the OS secret store (or the encrypted file store on headless machines). The vault is the list of enrolled devices' public recipients. Every file, and the index, is age-encrypted to *all* enrolled recipients (age supports several; it is already a dependency for the encrypted-file secret store). There is no shared password and no key server.

**Layout in the transport** (a directory or git tree):

```
vault.json          plain: format version, vault id, enrolled recipients (public keys, device names, added-at)
index.age           encrypted: file list (path, content hash, size, mode), a per-device counter, the last-writer's device id
objects/<sha256>.age   encrypted file contents, named by the hash of the ciphertext
```

Only recipients (public keys) and ciphertext are visible. The index carries the paths, so file names are hidden.

**Enrollment.** The new device runs `rigfile sync join <transport>`; it creates its identity and prints its recipient and a short fingerprint (six words). On an enrolled device, `rigfile sync approve <recipient>` shows the same fingerprint for you to compare (you are holding both machines, or read it over a call), adds the recipient, and re-encrypts the index and objects to the new set. There is no online exchange for an attacker to intercept: the recipient is a public key you carry yourself.

**Revocation.** `rigfile sync revoke <device>` removes a recipient and re-encrypts everything to the remaining set. History in a git transport still contains ciphertext the revoked device could decrypt; the guidance is to treat a revoked device's earlier contents as exposed and to rotate anything sensitive in them.

**What is synced.** The manifest's `private:` list (paths relative to the rig directory) plus, once you decide the mapping (Decision 2), Claude Code memory. Nothing else. A file is synced only if it is listed by *your own top-level* rig.

**Operations.** Explicit, no daemon at first: `rigfile sync status`, `push`, `pull`. `pull` writes through the journaled writer, so every overwritten file is backed up and `rigfile rollback` undoes a pull.

**Conflicts.** Each file records the content hash last synced from each device. If both sides changed a file since, neither wins silently: the incoming version is written next to it as `<name>.conflict-<device>-<date>` and `status` lists it. Markdown three-way merge is a later refinement, not the first version.

**Integrity and rollback protection.** The index is authenticated by age; it contains a per-device monotonic counter. A pull refuses an index whose counter for any device is lower than the highest this machine has seen (a provider serving an old snapshot). Deleting a file is a change in the index, not a deletion in the transport.

**Recovery.** Losing every device loses the vault, by design. Optional: `rigfile sync recovery` adds an age *scrypt* recipient (a long passphrase you print and store offline), so a new device can enrol without an old one.

**Scanning.** Every file passes the secret scanner before encryption. A hit blocks that file with the rule and line (never the value).

## 4. Option B: a registry-hosted store

The registry could store the encrypted objects for signed-in users (same crypto, transport replaced by an authenticated upload API). Pros: nothing to configure, works across any two machines with `rigfile login`. Cons: we then hold ciphertext and metadata about everyone's private files, must handle quotas, abuse and deletion, and a registry compromise becomes a data-availability incident (not a confidentiality one, given the crypto). I recommend **not** doing this first; the design above lets it be added later as one more transport with no change to the crypto or layout.

## 5. Decisions I need from you

| # | Decision | Recommendation |
|---|---|---|
| 1 | Transport: bring-your-own git or folder (A), or registry-hosted (B), or both | A first; B only if users ask |
| 2 | **What is "memory"?** Claude Code keeps per-project memory under `~/.claude/projects/<path-encoded-project>/memory/`, keyed by the project's absolute path, which differs per machine. Options: (a) sync only `private:` paths of a rig (no automatic memory); (b) also map a project to a stable id (git remote URL) and sync that project's memory dir between machines that have the same remote; (c) sync `~/.claude/CLAUDE.md` only | (a) then (c) in the first release; (b) after checking what Claude Code's memory format is (**UNVERIFIED**: I have not read the vendor's memory documentation for this design, and the plan forbids reading your real `~/.claude`) |
| 3 | Revocation guarantee: is "stops reading future writes, keeps the past" acceptable, with rotation guidance | yes; it is the honest limit |
| 4 | Recovery passphrase: offered, or omitted so that losing all devices loses the data | offer it, off by default |
| 5 | Conflicts: keep-both first, merge later | yes |
| 6 | Hosted transport rules if B: quotas, retention, deletion on account removal | decide only if B is chosen |

## 6. Build plan (after your decisions)

| # | Milestone | Acceptance |
|---|---|---|
| P-M1 | Vault format and crypto: create, enrol, revoke; encrypt/decrypt objects and the index to a recipient set | Golden vault files; age round trips; a revoked recipient cannot read new data; tampering and rollback are detected |
| P-M2 | `rigfile sync status|push|pull` over a directory transport, journaled writes, conflict files, scanner gate | Two simulated devices converge; conflicts keep both; a secret in a file blocks it; `rollback` undoes a pull |
| P-M3 | `join` / `approve` / `revoke` / `recovery` with the fingerprint check | Enrolment tests including a wrong fingerprint; no key ever printed |
| P-M4 | Git transport (a repo you own; never force-push, never rewrite history) | Real git in a container test |
| P-M5 | Memory mapping per Decision 2, `doctor` line, docs, red team (a malicious transport: rollback, swap, delete, forged recipient) | Red-team table like `docs/red-team-broker.md` |
| P-M6 | Owner gate | Two real machines; a real private repo |

Everything before P-M5 needs no vendor formats; P-M5 is the only part blocked on verifying Claude Code's memory layout from official docs.

## 7. As built

Implemented in `internal/vault` (crypto, roster, index, sync engine) and `cmd/rigfile/cmd_sync.go` (`rigfile sync ...`).

**Decisions taken (my recommendations):** transport is bring-your-own, a directory that may be a git repository you own (`--git`: fast-forward before, commit and push after; never force-push, never rewrite history); the registry is not a transport; conflicts keep both sides; the recovery passphrase is **not built** (losing every device loses the vault, as documented); revocation is "stops reading future writes, keeps the past". **Memory:** only files you `track` are synced: a file under your home (e.g. `~/.claude/CLAUDE.md`) or a path a rig lists under `private:` (`--rig <dir>`). Per-project Claude Code memory (mapping a project to a stable id) is **not built** and stays **UNVERIFIED** (the vendor's memory layout was not read, and Rigfile does not read your real `~/.claude` unless you track a file yourself).

**Cryptography.** Confidentiality: age X25519, every file and the index encrypted to every enrolled device. Authenticity: age does not authenticate a sender (anyone with public keys can produce valid ciphertext), so each device also has an Ed25519 key. The device list is a **signed hash chain** (`rosters/000001.json` ...): version *n* names the hash of *n-1* and is signed by a device that was enrolled in *n-1*. Each device **pins** the roster it trusts; a chain that does not contain the pinned version unchanged, or is shorter, is refused. The index is signed by its writer (who must be in the current roster), encrypted, and carries a per-device counter; a device remembers the highest counter it has seen and refuses lower ones (rollback). Every object's plaintext hash and size are in the signed index, so a substituted ciphertext is refused.

**Enrolment without a server.** `sync join` creates keys and posts a PUBLIC join request; it prints the device's fingerprint (five groups of four hex digits). On an enrolled device `sync approve <device> --fingerprint <that>` refuses a request whose fingerprint differs, so someone who can write to the storage cannot slip a device in. The approver prints the new **vault fingerprint** (a hash of the roster); `sync finish --fingerprint <it>` on the joining device pins the roster only if it matches. `revoke` re-encrypts the index and every object to the remaining devices and deletes the old ciphertext; `rekey` repairs an interrupted change.

**Local safety.** A device writes only to paths **it** chose with `track`; a name arriving from the vault can never create a file (`TestSyncScansTracksSafelyAndRefusesCredentialPaths`). Pulls go through the journaled writer (backup first, `rigfile rollback` undoes them). Files must be regular (no links or directories), at most 64 MiB, at most 5000 in a vault. `track` refuses well-known credential locations (`.ssh`, `.aws`, `.gnupg`, `.kube`, `.docker`, `.netrc`, `.npmrc`, `.env`, `id_*`, Rigfile's own state) and anything outside the home directory. **Every push runs the secret scanner**; a finding refuses the file (rule names only, never the value) unless you pass `--allow-secrets <name>`.

**`private:` and publishing.** A path under `private:` that the manifest also ships (an instruction, skill, agent or command inside it) is now a manifest **error**, so `validate`, `publish`, the registry's upload check and `apply` all refuse it. Files that are not referenced by the manifest were never published (the publisher copies only what the manifest names).

### Red team of a hostile storage (all in `internal/vault/vault_test.go`)

| The storage (or someone who can write to it) tries to | Result | Test |
|---|---|---|
| read the files or their names | ✔ only ciphertext and public keys are stored; a device's secret never is | `TestTheStorageSeesOnlyCiphertextAndPublicKeys` |
| serve an older snapshot | ✔ refused (counters) | `TestForgeriesAndRollbackAreDetected` |
| write its own index, encrypted to everyone | ✔ refused: not signed by an enrolled device | same |
| swap an object for another well-formed ciphertext | ✔ refused: the signed hash does not match; nothing is written | `TestASwappedObjectAndAForgedRosterAreRefused` |
| add its own device to the roster | ✔ refused: not signed by a member | same |
| rewrite the roster history | ✔ refused: does not contain the pinned version | same |
| post a join request and get it approved | ✔ refused: the fingerprint must match the joining device's screen | `TestEnrolmentNeedsTheRightFingerprint` |
| serve a different vault to a joining device | ✔ refused: the vault fingerprint must match | same |
| keep reading after a device is revoked | ✔ new data is not encrypted to it; old ciphertext is removed (git history keeps old data readable by it) | `TestRevokeReEncryptsEverythingToTheRemainingDevices` |
| leave a device change half-done | ✔ detected with a clear message; `rekey` repairs | `TestAnInterruptedDeviceChangeIsRepairedByRekey` |
| hide a deletion or an edit | — **not stopped**: a storage that withholds ALL new writes (or serves the current snapshot forever) looks like "no changes"; counters only prove it never goes backwards |  |
| learn when and how much you sync | — **not stopped**: file counts, sizes and times are visible |  |

Also tested end to end through the commands: two machines converging, conflicts kept apart (exit code 3), a revoked device shut out, and git as the transport (the repository holds `index.age` and objects, never a file name): `TestSyncTwoMachinesThroughTheCommands`, `TestSyncScansTracksSafelyAndRefusesCredentialPaths`, `TestSyncOverAGitRepositoryYouOwn`.

### Owner checks

1. Two real machines, a private git repository you own: `init`, `join`, `approve`, `finish`, `track`, `push`, `pull`, and confirm the fingerprints match on both screens.
2. Windows: `platform.WritePrivate` for the state files, path handling for tracked files, git available.
3. **Check what you track.** The scanner is heuristic: a personal `CLAUDE.md` can hold text you would not want on a second machine's disk.
4. Nothing was verified against the real Claude Code memory layout; decide whether you want per-project memory synced, which needs that verification first.
