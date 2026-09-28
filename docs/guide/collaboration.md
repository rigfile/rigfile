# Forks, collections and organisations

## Building on someone else's rig

Two ways, depending on whether you want your own copy or want to keep receiving the original's updates.

| You want | Run | What you get |
|---|---|---|
| Your own copy to change freely | `rigfile fork owner/name --name you/your-rig` | A copy of the rig renamed to yours, version reset to `0.1.0`, crediting the original in a comment. Later changes to the original do not reach it (`rigfile changes` shows them). |
| To stay on top of it | `rigfile fork owner/name --name you/your-rig --extend` | A small rig whose `from:` names the original (`owner/name@^X.Y`). Your changes layer on top; the original's compatible updates flow in on the next apply, subject to your lockfile. |

The source can be a registry rig (`owner/name[@version]`, with `--registry`), a git source, or, for a copy, a local rig directory. You can also add a line to `from:` by hand; each rig page shows a ready-made **Use as a base** snippet, and a **Built on this rig** list of public rigs that use it.

```sh
rigfile fork acme/python-dev --name you/python-dev --registry https://your-registry.example
cd python-dev && rigfile plan .
```

## Collections

A collection is a curated, ordered list of rigs: yours, other people's, or both, with an optional note for each. Use one to publish "starter rigs for data work" or to keep your own shortlist.

```sh
rigfile collection create starter-rigs --title "Starter rigs" --description "Good first rigs"
rigfile collection add starter-rigs acme/python-dev --note "Python with uv and ruff"
rigfile collection show you/starter-rigs
rigfile collection list
rigfile collection rm starter-rigs acme/python-dev
rigfile collection delete starter-rigs
```

Add `--private` on create to keep it to yourself. You can also create and edit collections on your profile page. A collection only ever shows each viewer the rigs that viewer is allowed to see: if a rig in it goes private or is removed, it quietly disappears from other people's view.

Limits: 50 collections per user, 100 rigs per collection.

## Organisations

An organisation is a shared namespace, like a GitHub organisation, for rigs a team publishes together: `acme/python-dev` instead of one person's `alice/python-dev`.

```sh
rigfile org create acme --title "Acme"
rigfile org add acme bob --role member      # bob must have signed in to that registry once
rigfile org members acme
rigfile org list                            # organisations you belong to
rigfile org rm acme bob                     # or yourself, to leave
```

Then name a rig `acme/<name>` in its `rigfile.yaml` and publish it as usual.

| Action | Personal rig | Organisation rig |
|---|---|---|
| See it while private, see pending and rejected versions, publish, yank | its creator | any member |
| Make it public or private | its creator | owners and admins |
| Add or remove members | — | owners: anyone, any role; admins: plain members only; anyone may leave |

An organisation always keeps at least one owner. Every member is fully trusted to publish under the organisation's name; there is no approval step. Limits: 10 organisations created per user, 200 members per organisation.
