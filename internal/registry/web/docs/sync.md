# Sync between your machines

Some files are yours alone and should follow you between machines, but never be published: a personal `CLAUDE.md`, notes, the `private:` paths of a rig. `rigfile sync` keeps them in an **end-to-end encrypted vault** in storage you already own: a private git repository or a synced folder. The storage only ever sees ciphertext.

## Set up

On the first machine:

```sh
rigfile sync init ~/vault --git          # ~/vault is a private git repository you own (or any synced folder, without --git)
rigfile sync track ~/.claude/CLAUDE.md
rigfile sync push
```

On each additional machine:

```sh
rigfile sync join ~/vault --git          # prints this device's fingerprint
```

Back on an enrolled machine, approve it, comparing the fingerprint with what the new machine's screen shows:

```sh
rigfile sync approve laptop --fingerprint XXXX-XXXX-XXXX-XXXX-XXXX
```

That prints the **vault fingerprint**. Finish on the new machine:

```sh
rigfile sync finish --fingerprint <vault fingerprint>
rigfile sync pull
```

## Day to day

```sh
rigfile sync status        # what would be pushed or pulled
rigfile sync push          # send your changes
rigfile sync pull          # receive theirs (backed up first; rigfile rollback undoes it)
rigfile sync devices       # enrolled devices
rigfile sync revoke laptop # remove a device and re-encrypt everything to the rest
```

Conflicts keep both versions side by side; nothing is overwritten silently.

## What it protects, and what it does not

- Files are encrypted to every enrolled device and signed by the device that wrote them. The storage (or anyone who can write to it) cannot read your files or their names, cannot slip in a device without matching fingerprints, cannot forge or swap content, and cannot roll you back to an older snapshot.
- `track` refuses credential locations (`.ssh`, `.aws`, `.gnupg`, `.kube`, `.docker`, `.netrc`, `.npmrc`, `.env`, keys) and anything outside your home directory. **Every push runs the secret scanner**; a finding refuses that file unless you pass `--allow-secrets <name>`.
- Not stopped: storage that withholds *all* new writes looks like "no changes", and file counts, sizes and times are visible to it.
- There is no recovery passphrase: if you lose every enrolled device, you lose the vault. Keep at least two devices enrolled.
- Files are at most 64 MiB each, 5000 per vault.
