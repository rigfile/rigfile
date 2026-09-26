# Collections (S8-M3)

A collection is a curated, titled list of rigs owned by one user ("Starter rigs", "Rigs for data work"). It holds **references**, never copies, so it cannot show a rig the viewer could not otherwise see.

## Rules

- **Owner and name:** `/c/{owner}/{slug}`; the owner is the user's login, so the namespace rules for rigs apply (nobody can create a collection under another user's name). Slug: lower-case letters, digits, hyphens, unique per owner among live collections (a deleted collection's slug is free again).
- **Visibility:** `public` (listed on the owner's profile, readable by anyone) or `private` (only the owner and admins). A private or missing collection is the same 404. A disabled user's collections are not shown to others.
- **Contents:** a rig can be added only if the *owner* can see it (their own rigs, public rigs). What a *viewer* sees is filtered again by the one rig visibility predicate at read time: a rig that later goes private, is removed, or has no published version vanishes from everyone else's view of the collection, without a placeholder or a changed count.
- **Limits:** 50 collections per user, 100 rigs per collection, title 80 characters, description 500, note per rig 200. The API is rate limited.
- **Audit:** create, add, remove and delete are written to the audit log.

## API

| Method and path | Auth | Purpose |
|---|---|---|
| `POST /v1/collections` | token | `{slug, title, description, visibility}` |
| `GET /v1/collections/{owner}/{slug}` | optional | the collection and the rigs the viewer may see |
| `GET /v1/users/{login}/collections` | optional | that user's collections the viewer may see |
| `PUT /v1/collections/{owner}/{slug}/items` | token (owner) | `{rig: "owner/name", note}`; again with a new note updates it |
| `DELETE /v1/collections/{owner}/{slug}/items/{rigowner}/{rigname}` | token (owner) | take a rig out |
| `DELETE /v1/collections/{owner}/{slug}` | token (owner) | delete |

Changing a collection needs a token whose user is the path's owner (403 otherwise); admins do not edit other people's collections here.

## CLI

`rigfile collection create <slug> --title "..." [--description "..."] [--private]`, `add <slug> <owner/name> [--note ...]`, `rm`, `delete`, `show <owner/slug>`, `list [user]`.

## Tests

`TestCollections` (creation and validation, adding rules, ownership, visibility of private collections and private rigs, rigs made private later, removal, deletion and slug reuse) and `TestCollectionLimits` in `internal/registry`; `TestRegistryCollectionsForksAndChangesFromTheCLI` in `cmd/rigfile`.
