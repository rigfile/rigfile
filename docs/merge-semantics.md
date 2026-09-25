# Merge semantics

**Status:** v1, accepted by the owner 2026-09-25 with all Appendix C recommendations as written (the **(decision)** items in this document are therefore settled; revisiting one needs a new ADR). **Date:** 2026-09-25.
**Scope:** how layers (`from:`), per-target overrides, `os:`/`targets:` filters and pre-existing user files combine into what `plan` shows and `apply` writes.
**Inputs:** `RIGFILE_PLAN.md` §6.3 (table), §8 (base-secure), §9.1 (structured merging); `schema/rigfile.v1.json`; vendor facts in `docs/targets/*.md`.
**Language-neutral.** Nothing here depends on ADR 0001.

Where this document goes beyond the plan, the sentence is marked **(decision)**; those are candidates for your sign-off.

---

## 1. Pipeline

```
resolve  →  merge (canonical, machine-independent)  →  project (this OS × this target)  →  reconcile with files on disk  →  plan
```

1. **Resolve** the `from:` graph to exact versions (recorded in `rigfile.lock`).
2. **Merge** all layers into one *canonical model*. This step knows nothing about the current OS, the installed targets, or the user's files. Consequence: the merged model, and its hash in the lockfile, is identical on macOS, Linux and Windows. **(decision)**
3. **Project** the canonical model onto (current OS, one target): apply `os:` and `targets:` filters, apply that target's `overrides`, and map to the target's capability matrix (unsupported → visible "skipped" entry).
4. **Reconcile** with what is already on disk (§8).
5. **Plan**: the change list. Nothing is written before the user approves.

Why merge before filtering: a `deny` rule attached to `os: [windows]` must still count as "an earlier layer's deny" for the purposes of §4.5, even while it is inactive on the current machine; and the lockfile must not depend on where it was resolved.

---

## 2. Layers

### 2.1 Order
- `from:` lists layers **low → high priority**. A later entry overrides an earlier one; the rig's own content is the highest layer.
- `rigfile/base-secure` is always the lowest layer regardless of where (or whether) it is listed. Listing it only pins its version. **(decision: plan says "always implied"; position in the list is ignored)**
- **Linearization:** depth-first post-order over `from:` (parents before children, siblings in listed order); a layer that appears more than once (diamond) is applied **once, at its earliest position**.
- **Cycles** are an error. **Maximum depth 8** (proposal, to bound resolution).
- **Version conflicts:** if two paths require the same rig with incompatible ranges, resolution fails unless one version satisfies all ranges; the lockfile records the single chosen version.

### 2.2 What is inherited and what is not
| Field | Rule |
|---|---|
| `name`, `version`, `description`, `license` | Not inherited (identity of this rig). |
| `targets` | Not merged. Each layer's `include`/`exclude` is checked independently: a detected target outside a layer's `include` produces a warning naming that layer. |
| `private` | Never published; unioned locally. |
| everything else | Category rules in §4. |

### 2.3 Per-target overrides (`overrides.<target>`)
For target *T*, each layer *L* contributes two consecutive layers: *L* itself, then *L*.`overrides.T` (if present). The sequence for a rig `top` on `mid` on `base` is therefore:

`base, base.overrides[T], mid, mid.overrides[T], top, top.overrides[T]`

The same category rules and locking apply to override layers. An override cannot weaken a locked item (§5) or remove a `deny` (§4.5).

### 2.4 Identity
The merge key of an item is **(category, id)**. Defaults when `id` is omitted: skill/agent/command = basename of `path` (without extension for agents/commands; folder name for skills); external skill `ref` = last path segment; instruction = required.
- Two items with the same identity **within one manifest** are an error.
- `os:` and `targets:` are *attributes*, not part of the identity. A later layer that redefines `(hooks, notify)` replaces the earlier definition for **all** OSes. To vary behavior by OS inside one item, use the per-OS `run:` object, not two items. **(decision)**
- Cross-category name collisions are flagged: a command and a skill both named `ship` create the same `/ship` in Claude Code; a `commands:` entry mapped to a Codex skill can collide with a skill of the same name.

---

## 3. Locked items (base-secure)

Every item that originates in `rigfile/base-secure` is **locked**:
- a later layer (or override block) may **add** items but may **not replace, remove, or disable** a locked item. An attempt is a **validation error**, not a silent no-op, so it fails loudly at `publish` and at `plan`;
- the only bypass is the local CLI flag `--i-understand-unsafe-base` (plan §8), which is never accepted by `publish`, and whose use is written to `state.json` and shown by `doctor` as a red item;
- `doctor` re-checks locked items on disk (drift = warning + one-key fix; plan §8.4).

Locking is by **layer identity** (`rigfile/base-secure`, verified by lockfile hash and, from Stage 6, signature), not by an in-manifest flag, so a third-party rig cannot mark its own items as locked or impersonate base-secure. The name `rigfile/*` is reserved (plan §10.3).

---

## 4. Rules per category

Legend: **U** union, **R** later replaces earlier (per identity), **W** whole-entry replace (no field merge), **L** locked items protected (§3).

| Category | Identity | Rule | Notes |
|---|---|---|---|
| instructions | `id` (+ `scope`) | ordered sections; **R** by id, **L** | §4.1 |
| skills | id | **U**, **R**, **L** | §4.2 |
| agents | id | **U**, **R**, **L** | §4.2 |
| commands | id | **U**, **R**, **L** | §4.2 |
| mcp_servers | name | **W**, **L** | §4.3 |
| hooks | id | **U**, **R**, **L** | §4.4 |
| permissions | rule (normalized) | **U** per list; deny wins; no removal | §4.5 |
| tools | name per manager | **U**; pin conflict → later wins + warning | §4.6 |
| secrets | ref path | **U**; `hosts` may only narrow | §4.7 |
| logins | provider | **U** | §4.7 |
| models | name | **W** | §4.8 |
| gateways | name | **W** | §4.8 |
| routing | scalar / list | scalar: later wins; `local_for`: ordered **U** | §4.8 |

### 4.1 Instructions
- Written as marked sections so Rigfile never edits user text. Marker format **(decision, extends plan §6.3)**:
  ```
  <!-- rigfile:begin <layer>#<id> sha256=<first 12 hex of content hash> -->
  …content…
  <!-- rigfile:end <layer>#<id> -->
  ```
  The hash lets `diff`/`doctor` detect hand-edits inside a managed section. Markdown is the only format that carries in-band markers; JSON/TOML/YAML use `state.json`-tracked keys (and comment markers where the format allows) because JSON has no comments.
- Same `id` in a later layer **replaces the content but keeps the earlier section's position**; section order is otherwise layer order, then file order. Stable output ⇒ stable diffs. **(decision)**
- `scope: user` and `scope: project` are separate namespaces: they target different files.
- The Codex `AGENTS.md` is capped at 32 KiB by default (`project_doc_max_bytes`, `docs/targets/codex.md` §3) and shares that budget with the user's own text: `plan` warns when merged sections approach the cap.
- If the destination has a shadowing file (Codex `AGENTS.override.md`), `plan` reports it and does not write.

### 4.2 Skills, agents, commands
- **U + R** by id; every replacement is listed in the plan ("`skills/pdf` from `jiaxu/python-dev` replaced by `jiaxu/data-science`").
- Skill *contents* are never merged file-by-file; a skill is an atomic directory. Replacement swaps the whole directory.
- Codex does **not** merge same-named skills (both appear in selectors). Rigfile therefore dedupes itself before writing.
- Codex has no command directory in current use (deprecated prompts): `commands:` is projected to skills for Codex (`docs/targets/codex.md` §4).
- Non-portable frontmatter (e.g. Claude-only `allowed-tools`) is carried through for the target that understands it; the plan shows "partial" for targets that would ignore it.

### 4.3 MCP servers
- Keyed by name; a later definition **replaces the whole entry**, matching Claude Code's own rule ("the entire server entry from that source is used; fields are not merged across scopes", `docs/targets/claude-code.md` §5).
- A same-name server that already exists in the user's own config (not written by Rigfile) is a **conflict**, resolved in the plan (default: keep the user's, skip ours).
- `overrides.<target>.mcp_servers.<name>` replaces the entry for that target only.

### 4.4 Hooks
- **U + R + L** by id. A later layer may replace a non-locked hook.
- **Ordering is not promised.** Claude Code runs all matching hooks in parallel and Codex loads all matching hooks; Rigfile can't provide ordering and must not let a rig depend on it.
- The generated native command for a hook must be stable per id, since Claude Code deduplicates identical handlers and Codex records trust by hook hash: a needless change re-triggers trust review.
- Events or handler types a target lacks are skipped with a visible note. Codex additionally requires the user to trust each hook (§7 of `docs/targets/codex.md`): merge produces the hook; `doctor` reports "present but untrusted".

### 4.5 Permissions (security-critical)
Each list (`deny`, `ask`, `allow`) is the **set union** of rules across layers, compared after normalization (canonical path form, trimmed whitespace). The rules below hold for any layer order.

1. **A layer can never remove a `deny`** contributed by an earlier layer. The manifest has no removal syntax, and none of the following can weaken one either: `overrides.<target>`, an `os:`/`targets:` restriction on a *different* rule, or a same-pattern `allow`.
2. **Evaluation order is deny → ask → allow, first match wins** (native to Claude Code; for Codex, "most restrictive wins" among `rules`). A later `allow` that is *exactly shadowed* by an earlier `deny` or `ask` (same kind and identical normalized pattern) is **dropped and reported** ("`allow read ~/.ssh/config` has no effect: denied by `rigfile/base-secure`"). Overlap analysis between different globs is not attempted: `plan` only claims shadowing when it can prove it (equality, or the deny pattern is a path prefix + `/**` of the allow pattern). Otherwise it shows both rules and lets the tool's native ordering decide.
3. `allow` is the dangerous list. In Codex an allowed command runs **outside the sandbox** (`docs/targets/codex.md` §8), so the Codex adapter refuses to project `allow` unless the item explicitly sets `targets: [codex]` **(decision)**.
4. A rule carries `os`/`targets` as attributes. The *set of denies* is computed on the canonical model, then filtered; a `deny` never disappears because an unrelated layer restricts itself to another OS.
5. Locked base-secure rules follow §3: cannot be replaced (they are set members, so "replace" would mean "remove").

### 4.6 Tools
- **U** per package-manager list; `common:` logical names resolve through `catalog/tools.yaml` per OS.
- Same tool pinned differently in two layers (`node@20` vs `node@22`): the **later layer wins** and the plan shows both **(decision)**. A pin and an unpinned entry: the pin wins.
- Escape-hatch per-OS blocks (`macos.brew`, `linux.apt`, `windows.winget`) union; only the block for the current OS is projected.

### 4.7 Secrets and logins
- **U** by ref path / provider. Same ref declared twice: `description`/`obtain_url` later wins.
- **`hosts` (owner host binding) may only narrow.** A later layer's `hosts` must be a subset of the earlier declaration; otherwise validation fails. Widening a binding would let a downstream rig redirect where a credential may be sent **(decision; the plan requires the binding, §7.3 rule 3, but does not say how layers combine)**.
- Secret **values** are never part of the model; a value found anywhere is a validation error.

### 4.8 Models, gateways, routing
- `models.<name>` and `gateways.<name>` are **W** (whole entry), like MCP servers **(decision; plan is silent)**.
- Merged result must satisfy: unique `serve.port` across models and gateway `listen` ports; loopback hosts only (schema); every `gateways.*.routes` key and `routing.fallback_on_limit` names a model in the merged `models`.
- `routing.default`: later wins. `routing.local_for`: ordered union. Routing is projected only where the target supports it; otherwise it is documented as a manual switch (plan §9.5).
- Hardware variant selection (`variants[].when`) happens at *project* time (it needs the machine), never at merge time.

---

## 5. `os:` and `targets:` filters
Applied at *project* time, after merge, with **AND** semantics (an item applies if the current OS is in `os` and the current target is in `targets`; an omitted key means all).
- Skipped-because-not-applicable items are listed in a collapsed "not applicable here" section of the plan (visible, not silent; plan §9.2).
- A hook whose per-OS `run:` object has no entry for the current OS is *applicable but unrunnable*: it is flagged on the plan screen (plan §6.2), not dropped.
- WSL is `linux` for filtering. Items meant for the Windows side use `os: [windows]` and are applied only through the Windows adapter when the user opted in (plan §9.4).

---

## 6. Locking file and determinism
- The canonical model is serialized with sorted keys and order-preserving arrays where order is meaningful (instruction sections, `from`, `local_for`) and sorted arrays where it is not. Its sha256 is stored in `rigfile.lock` next to each layer's resolved version and content hash.
- `apply` refuses a layer whose hash does not match the lockfile (plan §6.4).
- Merge must be a pure function of (lockfile, rig files): no clock, no environment, no machine facts.

---

## 7. Worked examples

**A. Permissions.** `base-secure` denies `read ~/.ssh/**` and asks `bash git push*`. `python-dev` adds `allow read ~/.ssh/config` and `deny bash "rm -rf /*"`. `data-science` adds `allow bash "git push*"`.
Result: deny = {`~/.ssh/**`, `rm -rf /*`}; ask = {`git push*`}; allow = {(`~/.ssh/config` dropped and reported: shadowed by the base deny)}. The `allow bash git push*` is kept in the list but the `ask` for the same pattern is evaluated first, so pushes still prompt.

**B. Locked hook.** `data-science` defines hook `block-env-commit` (an id owned by `base-secure`). **Error:** "hook `block-env-commit` is locked by `rigfile/base-secure`; add a new id or remove the definition." Rename it and both run.

**C. MCP replace.** `python-dev` defines `github` (http, oauth). `data-science` defines `github` (stdio, pinned). Result: the stdio entry replaces the http entry entirely; no `url` or `auth` leaks through. The plan shows "replaced `github` from `python-dev`".

**D. Diamond.** `top` from `[b, c]`, both from `d`. Order: `base-secure, d, b, c, top`. `d` is applied once.

**E. Override.** `top.overrides.codex.permissions.deny: [read: "~/.codex/auth.json"]` adds a deny for Codex after `top`. It cannot remove anything from earlier layers.

---

## 8. Reconciling with files already on disk

Every write goes through backup + structured merge (CLAUDE.md rule 6).
- **Rigfile-owned regions:** marked sections (Markdown) and keys/tables recorded in `~/.rigfile/state.json` (JSON/TOML/YAML). Updates replace only these regions.
- **User-owned content in the same file:** never modified. A collision (same key/table/section id, not recorded as ours) is a **conflict** shown in the plan; default is *keep user's, skip ours*.
- **Formats:** structured edits must preserve comments and key order where the parser allows (Codex `config.toml` is hand-edited). Editing a JSON file another program is also writing (Claude Code's `~/.claude.json`) is unsafe while it runs; prefer the vendor's own writer (`claude mcp add-json`) when it exists (`docs/targets/claude-code.md` §5).
- **Drift:** an edit inside a managed region changes its hash; `diff` shows it and `update` asks before overwriting.

---

## Appendix A: validator rules that JSON Schema cannot express

These belong to the manifest-validation module (name and language pending ADR 0001). The schema already enforces shapes, loopback hosts, `https://` URLs, no hard-coded home paths, credential-named env vars/headers must be `secret://`, and a cheap floor of "known secret shapes" in free text.

1. **Real secret detection** in every field *and every referenced file* (instructions, skills, hook scripts, agents), using the `scan` module (gitleaks-style rules + entropy). The schema's regex floor is not a substitute.
2. **Pinning.** Parse `mcp_servers.*.command/args` for `npx`, `bunx`, `uvx`, `pipx run`, `docker run`, `node`/`python -m` launchers and require an exact version; external skill `ref`s; `tools` where the package manager supports pins; `models.*.variants[].engine_version` and `revision` (40-hex SHA for Hugging Face repos). Unpinned → warning locally, **rejected on public publish** (plan §6.2). *Recommendation:* add an optional structured `package: {manager, name, version}` field to MCP servers so pins are checkable without parsing free-form args.
3. **Locked items** not redefined (§3).
4. **Cross-references:** `routing.fallback_on_limit`, `gateways.*.routes` keys ∈ `models`; `local_for: subagent:<name>` names an agent in the merged model; `logins` providers referenced by servers.
5. **Uniqueness:** duplicate ids per category within one manifest; duplicate ports; and **case-insensitive path collisions** (`Skills/A` vs `skills/a` collide on macOS and Windows).
6. **Files:** every `rigPath` exists, stays inside the rig after symlink resolution, has no CRLF-dependent behavior, is within size limits; each skill contains a `SKILL.md` with `name` and `description`; each agent file has required frontmatter.
7. **Capability checks:** item categories unsupported by a declared target produce a warning at validate time (from `adapters/<target>/capabilities.yaml`).
8. **Egress/secret consistency (warn):** every `secret://` used by a server's `env`/`bearer_token` has `hosts` that overlap that server's `network.allow`.
9. **Publish-only rules:** no `--i-understand-unsafe-base` effects; reserved names (`rigfile/*`, vendor names); `private` paths excluded from the tarball; no unpinned executables.
10. **Model safety:** weight formats limited to safetensors/GGUF/MLX (schema `weights_format`); `trust_remote_code` is not expressible in a rig at all.

## Appendix B: proposed schema changes (not in plan §6/§9.5)

Each is in `schema/rigfile.v1.json` and marked `PROPOSED` or explained in a `description`. All are additive; remove any you dislike.

| Addition | Why (research-driven) |
|---|---|
| `mcp_servers.<n>.auth: bearer` + `bearer_token: secret://…` | Both tools have a first-class bearer mechanism (Codex `bearer_token_env_var`/`http_headers_helper`; Claude Code headers). Without it, a bearer token has no place to live except a literal header. |
| `secrets.<ref>.hosts` | Plan §7.3(3) and §7.2 require a declared owner-host binding but §6.1 had no field. |
| `models.*.serve.api` ∈ `openai-chat` / `openai-responses` / `anthropic-messages` (`openai` = alias of `openai-chat`) | Codex custom providers accept only the Responses API; Claude Code needs Anthropic Messages; `mlx_lm.server` speaks only chat completions (`docs/targets/codex.md` §9). |
| `gateways.*.expose` | The direction of the bridge determines which agent can use it. |
| `models.*.variants[].weights_format` | Makes the plan's "safe formats only" rule machine-checkable. |
| `hooks[].timeout_seconds` | Both tools have per-hook timeouts. |
| Permission rule kinds `web_fetch`, `mcp`, and `edit` in addition to `read`/`bash` | Claude Code has `WebFetch(domain:)`, `mcp__…` rules and separate `Edit`; Codex MCP per-tool approval modes. |
| `targets.exclude`, `login.os`, `tools.{uv,cargo,go}`, `tools.linux.{pacman,zypper,brew}`, `tools.macos.cask` | Completeness for the platform table; harmless if unused. |
| `instructions[].merge` limited to `section` | Explicit that other strategies (owned file + `@import`) are undecided. |

## Appendix C: open questions raised here
1. Marker format with content hash (§4.1): accept?
2. Same-id instruction replacement keeps the *earlier position* (§4.1): accept?
3. Tool pin conflict: later wins with warning vs error (§4.6).
4. `hosts` may only narrow (§4.7): accept?
5. `models`/`gateways` whole-entry replace (§4.8): accept?
6. Codex `allow` refused by default (§4.5.3): accept?
7. Should the structured `package:` field for MCP pins be added to the schema (Appendix A.2)?
