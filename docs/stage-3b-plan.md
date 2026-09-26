# Stage 3b plan of record: local models

**Goal (RIGFILE_PLAN.md §12, §9.5):** a rig can install, serve, and wire in a local LLM chosen for the machine's hardware.

Started 2026-09-26 on branch `stage-3b`, created from `main` (Stages 1-8 merged or pushed; Stage 3b was skipped when Stage 4 went ahead).

## The plan's exit criterion cannot be met as written, so it is re-scoped

Stage 0's research (`docs/targets/codex.md` §9, `claude-code.md` §9, `catalog/models.yaml`) found:

- `mlx_lm.server` speaks OpenAI *chat completions* only. **Codex** custom providers accept only the *Responses* API (the docs are internally inconsistent: UNVERIFIED), and `--oss` supports only Ollama and LM Studio. **Claude Code** needs an Anthropic *Messages* endpoint and Anthropic says routing it to non-Claude models is unsupported. So the reference setup (mlx-lm + Qwen3-8B-4bit) is **not reachable from either agent** without a bridge, and no bridge (LiteLLM or other) was verified.
- **Ollama** is the one engine with a documented path to both: Codex `--oss` / `oss_provider = "ollama"`, and an Anthropic-compatible `/v1/messages` that Ollama's docs say Claude Code can use.

## What I build (decisions by my recommendation)

1. **Two engines, both real:** `ollama` (all OSes) and `mlx-lm` (Apple Silicon reference setup). `llama.cpp` stays a catalog stub (nothing researched: **UNVERIFIED**, never applied).
2. **No gateway.** `gateways:` keeps parsing and merging, but is *not applied*; the plan screen says so. Building on an unverified, vendor-unsupported bridge would fail silently.
3. **No global agent configuration is written.** Wiring is explicit and reversible:
   - `rigfile models run codex` → `codex --oss --local-provider ollama -m <model>` (documented flags), Ollama only.
   - `rigfile models run claude` → `claude` with `ANTHROPIC_BASE_URL` (and model-pin variables) for that process only, **Ollama only, labelled experimental and vendor-unsupported** on screen.
   - Any OpenAI-compatible tool, and a rig's own hooks and scripts: `rigfile models url <name>` prints the endpoint (this replaces the plan's `RIGFILE_LOCAL_LLM_URL` variable, which nothing would set for a hook launched by an agent).
   - mlx-lm ↔ Codex or Claude Code: shown as "needs a bridge: not supported", never wired.
4. **Nothing large downloads by surprise.** The plan screen shows the chosen variant, publisher, license, revision, download size and free disk. `--models now|later|skip` (default: ask on a terminal, `later` with `--yes`).
5. **Downloads are pinned and verified.** Hugging Face models: fetched at a 40-hex revision, every LFS file verified against the size and sha256 the API reports, pickle-based formats refused. Ollama models: pulled by tag, digest recorded and compared to a pinned digest when the catalog has one (else a visible warning).
6. **Safe by rule** (base-secure additions): safe weight formats only (safetensors, GGUF, MLX safetensors), no `trust_remote_code`, servers bind loopback only (validated in the manifest, in the generated command, and by `doctor`), stub or `TODO-verify` catalog entries are never applied.
7. **The model server is a per-user service** through one generic service generator (launchd, `systemd --user`, scheduled task; generated files, injectable activator, journaled writes: the same design as `rigd`).
8. **`doctor`** checks: server up, loopback only, a chat completes, a tool-call smoke test (mark "chat only" on failure).
9. **`rigfile init`** notices a running Ollama or `mlx_lm.server` on loopback and offers a `models:` entry (it probes the network port only; it does not read any config folder).

## Milestones

| # | Milestone | Acceptance | Status |
|---|---|---|---|
| S3b-M0 | this plan and `docs/models.md` | every rule has a named test | done (plan); spec with M2 |
| S3b-M1 | Hardware detection (`internal/platform`), catalog loader, variant selection, safety validation (`internal/models`) | parsers per OS from fixtures; selection and rejection tables; stubs never chosen | todo |
| S3b-M2 | Manifest integration: `models:` (role or variants) resolved in `session.Prepare`, MODELS plan section, `rigfile models plan|list`, `--models` flag | goldens; loopback and format refusals | todo |
| S3b-M3 | Downloads: pinned Hugging Face fetcher with hash verification, Ollama pull, engine install through the tools plan | fake HTTP server tests: bad hash, wrong size, pickle refused, resume | todo |
| S3b-M4 | Service: generic generator, install/start/stop/status, apply and rollback integration | golden files per OS, fake activator | todo |
| S3b-M5 | Wiring: `rigfile models run codex|claude|url`, plan-screen support matrix | real child processes with fake binaries | todo |
| S3b-M6 | `doctor` checks, capture from a running server, docs | fake servers | todo |
| S3b-M7 | Owner gate: `docs/stage-3b-owner-checks.md` | live checks on Apple Silicon, Linux, Windows; real downloads | todo (owner) |
