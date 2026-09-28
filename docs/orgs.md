# Organisations and team registries (S8-M4)

An **organisation** is a namespace several people publish under: `acme/tool` instead of `ada/tool`. Its private rigs are visible to its members, so the registry doubles as a team's private registry with no separate server.

## Model

- `orgs(login, name, created_by, disabled_at)`, `org_members(org, user, role)` with roles `owner`, `admin`, `member`, and `rigs.org_id` for a rig an organisation owns.
- A rig published under an organisation's name (`acme/...`) belongs to the organisation from its first version: **membership, not authorship, is the access control**. Whoever uploaded first has no special standing; if they leave, they lose access.
- **One namespace.** People and organisations share the `owner` part of `owner/name`. The database enforces it with triggers (with an advisory lock so two simultaneous claims take turns): a person cannot sign in under an organisation's name, an organisation cannot take a person's name, and the reserved names (`rigfile`, vendors) are refused for organisations too.

## Who can do what

| Action | Personal rig | Organisation rig |
|---|---|---|
| See it when private, see pending and rejected versions, publish versions, yank | its creator | any member |
| Change visibility (public/private) | its creator | owners and admins |
| Add or remove members | n/a | owners: anyone, any role; admins: plain members only; anyone may leave |
| Anything, everywhere | site admins | site admins |

An organisation always keeps at least one owner (the last owner can neither leave nor be demoted). Members are added by login and must have signed in to the registry once. Limits: 10 organisations created per user, 200 members per organisation, the API is rate limited.

## Visibility, precisely

`rigMember` is the single definition of "belongs to this rig": `CASE WHEN org_id IS NULL THEN created_by = viewer ELSE viewer is in org_members END`. It replaces "the viewer created it" inside the two visibility fragments, so **every read path inherits it** (rig and version APIs, diffs, derived lists, collections, search, profiles, tarball download). `NOT EXISTS (disabled org)` is added to the rig fragment: a disabled organisation's rigs are visible to site admins only (`rigfile-registry admin disable-org`). Things that are *not* membership-based on purpose: search and derived lists show public rigs only, and a collection filters each rig through the same predicate for the *viewer*, never the collection's owner.

Missing and private stay indistinguishable: a rig, an organisation's member list, or a member the caller cannot see is the same 404.

Trust facts for an organisation's rig name the organisation as publisher (never "verified", never the member who happened to upload first); publisher counts and moderation history use the organisation's rigs, and personal counts exclude them.

## Web

On their own profile a signed-in user can create an organisation. On an organisation's page (`/u/<org>`) members see the member list; owners and admins also get the add-or-change form (owners may choose any role, admins only `member`) and remove buttons; anyone gets a *leave* button for themselves. The forms post to `/manage/orgs...` and call the same store methods as the API, so the role rules are the API's (`TestWebFormsForOrganisations`: a plain member is refused when trying to appoint or remove, and after leaving no longer sees the list).

## Threat note

| Risk | Handling |
|---|---|
| Name squatting or impersonation (an org called like a person or a vendor) | shared-namespace triggers, reserved names, a creation cap per user, admin `disable-org` |
| A departed member keeps access | there is none to keep: access is a live membership lookup on every read; no token or session carries an organisation grant |
| The member who created a rig treated as its owner | `rigMember` looks at membership only for organisation rigs |
| Privilege escalation inside an organisation | admins cannot touch owners or admins or appoint anyone above member; the last owner is protected; all changes are audit-logged (`org.create`, `org.member`, `org.member.remove`) |
| Existence oracles | outsiders get identical 404s for a private rig, a missing rig, an unlisted organisation's members |
| A private rig leaking through another feature | collections, diffs, derived lists and search all go through the predicate; `TestOrganisations` checks each |

Not built: invitations (a member must already have an account), transferring a personal rig to an organisation, per-rig roles, single-sign-on.

## Tests

`TestOrganisations` (namespaces, roles, publishing, visibility through every route including collections and diffs, visibility and yank rights, leaving), `TestNamespaceRaceHasOneWinner`, `TestDisabledOrganisationVanishes` in `internal/registry`; `TestRegistryOrganisationsFromTheCLI` in `cmd/rigfile`.
