# Rigfile status

**Current stage: Stage 0 — Research & spec freeze — DRAFTS COMPLETE, AWAITING OWNER REVIEW / SIGN-OFF**
Last updated: 2026-09-25

Stage 0 exit criteria (RIGFILE_PLAN.md §12): schema reviewed; target docs complete for Claude Code + Codex at minimum; owner signs off. **Not yet met: needs review and sign-off.** Stage 1 must not start until then.

## Stage 0 checklist

- [x] 1. Repo skeleton: `CLAUDE.md`, `docs/STATUS.md`, `.gitignore` (base-secure §8.1a rules + existing Python rules), language-neutral dirs, `git init` (repo already existed; no commits made)
- [x] 2. `docs/targets/claude-code.md`, `docs/targets/codex.md` (official docs, checked 2026-09-25, every claim linked; CONFLICT / UNVERIFIED marked)
- [x] 3. `docs/platforms.md` (from §9.4, corrected)
- [x] 4. `schema/rigfile.v1.json` (validated: 0 errors on the fixture, 29 accept/reject probes behave as intended) and `docs/merge-semantics.md`
- [x] 5. `catalog/tools.yaml` (gh, uv, jq, git, node, gitleaks) and `catalog/models.yaml` (mlx-community/Qwen3-8B-4bit + `mlx_lm.server …` as given)
- [x] 6. `docs/adr/0001-language.md` (status: Proposed; recommends Go with a Stage-1 spike and a Python fallback)
- [ ] Owner review of: schema, merge-semantics decisions (Appendix C), ADR 0001
- [ ] Owner sign-off

Also added: `testdata/fixtures/plan-example.rigfile.yaml` (the plan's §6.1/§9.5 example, adapted to validate; illustrative values only).

## Not done in this session (deliberate)

- Targets Cursor, Gemini CLI, Claude Desktop: no `docs/targets/*.md` (plan requires only Claude Code + Codex for the exit). Claude Desktop paths for macOS/Windows are in `docs/platforms.md`.
- Sanitized fixture of the owner's own Mac setup (§12 Stage 0 bullet): requires reading real config folders, which this session was told not to do. Needs the owner to supply a sanitized export or authorize a scoped read later.
- `cmd/`, `internal/`, `web/`: not created; they encode the Go layout and wait for ADR 0001.

## Verification results / known gaps

- UNVERIFIED items are listed at the end of each `docs/targets/*.md` (Windows stdio MCP `cmd /c`, Codex `wire_api = chat`, Windows `CODEX_HOME`, MCP env inheritance, per-subagent endpoints, etc.). None should be relied on before a test.
- ADR 0001 benchmarks are from one Apple M2 Mac (hello-world programs). Linux/Windows start-up not measured.
- One check was not completed: GoReleaser's current Scoop / Homebrew-formula docs pages (tooling error). Recorded in ADR §8.

## Notes

- A Python scaffold (`pyproject.toml`, `src/`, `tests/`, `README.md`) existed before Stage 0 and is not part of the plan. Left untouched pending ADR 0001.
- Schema `$id` (`https://rigfile.dev/schema/rigfile.v1.json`) is a placeholder; the domain is not claimed (§17 Q6).

## Next (proposed, do not start)

Owner decisions → if ADR accepted: Stage 1 spike (ADR §7) → Stage 1 proper.
