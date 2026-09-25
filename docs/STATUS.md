# Rigfile status

**Current stage: Stage 1 — Local CLI, Claude Code only, no network, macOS + Linux — IN PROGRESS (spike passed; building the full stage)**
**Stage 0: COMPLETE, signed off by the owner 2026-09-25.**
Last updated: 2026-09-25

## Stage 0 result (signed off)

Deliverables (all in `main`): repo skeleton and working agreements (`CLAUDE.md`); target research for Claude Code and Codex (`docs/targets/`); `docs/platforms.md`; `schema/rigfile.v1.json`; `docs/merge-semantics.md`; `catalog/tools.yaml`, `catalog/models.yaml`; `docs/adr/0001-language.md`; fixture `testdata/fixtures/plan-example.rigfile.yaml`.

Sign-off terms: the owner accepted the schema and the seven merge-semantics decisions (Appendix C of `docs/merge-semantics.md`) as recommended. The schema was validated mechanically (0 errors on the fixture; 29 accept/reject probes), not line-by-line reviewed by the owner; expect schema changes during Stage 1 (additive changes are cheap, breaking ones need a note here).

## Decisions log

| Date | Decision | Source |
|---|---|---|
| 2026-09-25 | **Language: Go**, conditional on the Stage 1 spike (fallback: Python `--onedir`) | ADR 0001 (Accepted) |
| 2026-09-25 | **Secret backend for Stage 1: OS keychain only** (macOS Keychain, Linux Secret Service, encrypted-file fallback for headless Linux). 1Password/Bitwarden later | plan §17 Q4 |
| 2026-09-25 | **OS order: macOS + Linux in Stage 1, Windows in Stage 3** (platform layer built for all three from day one; Windows stubbed) | plan §17 Q8 |
| 2026-09-25 | Merge-semantics decisions 1–7 accepted as written | `docs/merge-semantics.md` App. C |
| 2026-09-25 | **Go confirmed** after the spike (go/no-go: go) | `docs/spike-report.md`, ADR 0001 |
| 2026-09-25 | **User-scope MCP written via `claude mcp add-json --scope user`**, not by editing `~/.claude.json` | ADR 0002 |
| 2026-09-25 | Owner allows a **read-only `rigfile init` capture of their real `~/.claude`** into a scratch dir to build the first sanitized fixture; nothing enters the repo before the owner reviews it | owner, 2026-09-25 |

## Still open (not blocking the Stage 1 spike)

- §17 Q1 open source / licence: **no LICENSE file yet** (default = all rights reserved). Needed before the repo is public or CI is added. The repo already has a GitHub `origin` remote; nothing has been pushed from this environment.
- Q2 business model, Q5 private memory sync (default: defer to Stage 8), Q6 domains/names (schema `$id` is a placeholder), Q7 targets after Claude Code (Stage 3), Q9 WSL, Q10 code-signing, Q11 local-model scope.
- Plan corrections found in Stage 0 that the owner has not yet folded into `RIGFILE_PLAN.md` (see the "Summary of plan corrections" tables in `docs/targets/*.md` and `docs/platforms.md` §7): notably §9.5 local-model wiring, Codex hook trust, Codex/Claude Code permission syntax, `${CONFIG_DIR}`, credential files in the deny list.
- Not done in Stage 0: Cursor / Gemini CLI / Claude Desktop target docs (Stage 3); sanitized fixture of the owner's own setup (needs an export from the owner).

## Stage 1 spike — result (branch `stage-1-spike`, not merged)

Built and tested 2026-09-25: manifest validation, marker-splice editing, JSON layout-preserving edits, platform layer, Claude Code permissions adapter with backup-first writes, secrets (keychain + age fallback), exec shim, PreToolUse guard, CLI. 179 tests, race-clean, no skips on macOS. Hook ≈ 5 ms/call; release binaries 4.7–5.8 MB on darwin/arm64, linux/amd64, linux/arm64, windows/amd64. Details, findings and the not-verified list: `docs/spike-report.md`.
**Owner confirmed go.** Still open from the spike: real-Keychain and real-Linux checks, Linux/Windows latency, file-store lock.

## Stage 1 — plan of record

Goal (plan §12): recreate the owner's setup on a fresh machine from a local folder, on macOS and Linux.

1. **Spike (ADR 0001 §7), 2–3 days:** Go vertical slice the owner reviews, then go/no-go on Go.
   - `plan` for a Claude Code `settings.json` permissions merge (JSON key order preserved)
   - keychain set/get on macOS + Linux (desktop keyring and headless `age` fallback)
   - `rigfile hook pre-tool-use` shim, latency measured on macOS and Linux
   - marker-splice edit of a TOML file with comments
2. Then the Stage 1 build per plan §12 (platform layer, `init/plan/apply/diff/rollback/doctor/secrets/exec`, Claude Code adapter, backups, state, lockfile, tools installer, CI matrix, E2E in clean VMs).

Working rules for Stage 1 are in `CLAUDE.md` (small PRs, threat note for security-sensitive code, no real secrets, tests use a temp `$HOME`).

## Notes

- A Python scaffold (`pyproject.toml`, `src/rigfile`, `tests/`, `README.md`) existed before Stage 0 and was committed by the owner as `42bd6bc "stage 0"` (branch `stage-0-spec`, also pushed to `origin`). With Go chosen it is not the product; keep as dev tooling or delete: the owner's call. It is not part of the Stage 0 deliverables.
- `origin/HEAD` currently points at `stage-0-spec` (first branch pushed). Local `main` is created from it at sign-off; pushing `main` and making it GitHub's default branch is left to the owner.
- Global secret-scanning git hooks (gitleaks) are installed on the owner's machine outside this repo (`~/.config/git/hooks`); they run on commits and pushes here. This is a stopgap until base-secure (Stage 2).
