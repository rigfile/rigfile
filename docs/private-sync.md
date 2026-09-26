# Private sync between your own machines: design (S8, deferred item)

Status: **design for your decisions; no code.** RIGFILE_PLAN.md §6 gives a manifest key `private:` ("never published; synced only to owner's machines") and §12 lists "private memory sync between the owner's own machines (end-to-end encrypted)" under Stage 8. `private:` is already in the schema and is honoured by publish (never included). Nothing syncs yet. This document says how it should, and where your decisions change the design.

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
