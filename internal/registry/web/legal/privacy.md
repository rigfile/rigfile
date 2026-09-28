*Draft for review. Have a lawyer review this text before the registry opens to the public.*

This notice explains what this registry stores about you, why, where, for how long, and what you can do about it. It describes what the software actually does. Questions: **bytebuilderslab@gmail.com**.

## Who we are

Byte Builders Lab ("we") runs this registry. Contact: bytebuilderslab@gmail.com.

## What we store

**When you sign in with GitHub**, we receive your public GitHub profile: numeric account id, login, display name and avatar address. We do **not** request any GitHub permissions, and we do not receive your e-mail address, your repositories or your private data.

**What you do here:**

- Rigs you publish: their files, manifest, README, description, versions, visibility, and the results of our automatic scan and static analysis.
- Stars, collections (and the notes you add), organisations you create or belong to, and your role in them.
- Reports you send: the rig, the reason and the details you write.
- If you sign a rig: the signing identity from your signature (for example a GitHub Actions workflow address, or the e-mail address a Sigstore signature was issued to), shown on the rig's page. Signatures are also recorded in Sigstore's own public transparency log, which we do not control.

**Facts shown next to your rigs** so others can judge them: when your account was first seen here, how many public rigs you have, whether an administrator verified you, and counts of yanked or moderator-removed versions. An administrator's private note about how you were verified is never shown.

**For security and abuse handling**, an audit log records who did what and when (for example sign-ins, publishing, visibility changes, reports and administrator actions).

**Sign-in data:** a session cookie that keeps you signed in for up to 7 days, a sign-in cookie that lasts 10 minutes, and command-line tokens that expire after 30 days. We store sessions and tokens only as one-way hashes. There are **no analytics, advertising or tracking cookies**, and the pages run no scripts.

**Logs:** our service logs each request's method, path (never the query string), status and duration. It does not log IP addresses, cookies, tokens or request bodies. To limit abuse, the service keeps short-lived counters keyed by IP address in memory only; they are never written to disk. Our hosting provider's network necessarily handles your IP address to deliver traffic, under its own policy.

## Why

To run the service you asked for (your account, publishing, pulling, stars, collections, organisations), to keep it safe (scanning, rate limits, the audit log, moderation), and to help people decide whether to trust a rig (the facts above). We do not sell your data or use it for advertising.

## Who processes it for us

- **Fly.io** hosts the service (United States, east).
- **Neon** hosts the database, including its backups.
- **Cloudflare R2** stores the rig archives.
- **GitHub** handles sign-in.

When a rig names MCP server packages, we look up their exact names and versions in the public **OSV** vulnerability database (no personal data is sent). To check signatures we fetch Sigstore's public trust data.

## How long we keep it

- Your account data and what you published, for as long as your account exists (a removed rig or version stops being shown).
- A rejected upload's archive is deleted at once; the reason stays visible to you.
- Database backups roll off automatically after a limited period (at most 30 days).
- Audit log entries are kept for security for as long as the service runs, unless you ask us to delete yours and no legal or security reason prevents it.

## Your choices and rights

- Keep a rig private, make it private again, or ask us to remove it.
- Ask us for a copy of the data we hold about you, to correct it, or to delete your account: write to bytebuilderslab@gmail.com from, or mentioning, your GitHub account. There is no self-service deletion yet; we will act within 30 days.
- Revoke the registry's access at any time in your GitHub settings (Applications → Authorized OAuth Apps). This does not delete your account here; ask us for that.
- Depending on where you live (for example the EU, the UK or California), you may have further rights, including to complain to your data protection authority.

## The Rigfile command-line tool

The CLI runs on your computer and sends **no telemetry**. It contacts the network only for what you ask it to do: this or another registry, git hosts for rigs you pull, the AI-tool vendors you sign in to, package and vulnerability lookups, model downloads, and update checks. Secret values stay in your own keychain and are never sent to the registry.

## Children

The registry is for developers and is not directed at children under 16.

## Changes

This is a draft for a beta service. We will update this page when things change and note the date here.
