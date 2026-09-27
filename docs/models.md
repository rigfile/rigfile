# Local models: spec as built (Stage 3b)

Status: implemented in `internal/models`, `internal/svc` and `cmd/rigfile/cmd_models.go`. Background: RIGFILE_PLAN.md §9.5; research: `docs/targets/codex.md` §9, `docs/targets/claude-code.md` §9, `catalog/models.yaml`. Every rule below has a named test.

## 1. What a rig says

```yaml
models:
  local-coder:
    role: local-coder            # resolved through catalog/models.yaml for THIS machine, or:
    variants:                    # explicit variants: first that matches and is safe wins
      - when: { os: macos, arch: arm64, min_memory_gb: 16 }
        engine: mlx-lm
        engine_version: "0.31.3"
        model: mlx-community/Qwen3-8B-4bit
        revision: <40-hex commit>
      - when: {}
        engine: ollama
        engine_version: "0.34.4"
        model: qwen3:8b
        digest: 500a1f067a9f     # Ollama: the id a pull must match
    serve: { port: 8080, autostart: true }
    license_ack: apache-2.0
```

`when` keys: `os`, `arch`, `gpu` (`nvidia`, `apple`), `min_memory_gb`, `min_vram_gb`, `min_disk_gb`. **Fail closed:** an unknown key never matches, and a minimum is unmet when the machine's value could not be read (`TestMatchesFailsClosed`). Apple Silicon reports its unified memory as GPU memory, and a Rosetta-translated x86-64 process on an M-series Mac still counts as arm64 (`TestRosettaOnAppleSiliconIsStillArm64`).

## 2. What is refused (the security rules)

`models.Validate` (`TestValidateRefusesWhatIsNotSafeOrPinned`, `TestFetchRefusesUnsafeRepositories`):

- catalog **stubs** and any `TODO` value are never applied;
- engines other than **`ollama` and `mlx-lm`** are never applied (llama.cpp and vLLM are schema names only; nothing about them was verified);
- weight formats other than **safetensors, GGUF, MLX safetensors**; `trust-remote-code`, `adapter-path`, `host`, `port` and `model` may not appear in `args`;
- servers listen on **loopback only**: refused in the manifest (schema), in the plan, in `Setup`, in `models serve`, and checked by `doctor` (§7);
- a Hugging Face model must be pinned to a **40-hex commit** and an engine version; an Ollama model is pinned by **digest** when the rig or catalog gives one (a mismatch after the pull is a hard error naming `ollama rm`; no digest is a visible warning).

## 3. Downloads (`internal/models/hf.go`)

Hugging Face models are fetched from the API at the pinned commit: the file list, then each file, verified against what the API reports for *that commit*: LFS files by size and SHA-256, small files by git blob id. Before any byte is downloaded the listing is refused if it contains pickle-based files (`.bin .pt .pth .pkl .pickle .ckpt .npy .npz`), Python source (a `trust_remote_code` model), a path that is not a clean relative one, or no weights of the declared format; `config.json` is fetched first and refused if it has `auto_map`. Partial downloads resume (Range) and a corrupt one is discarded, never resumed; a verified file is not downloaded again; nothing is left at its final name unless it verified. The read token, if any, is sent only to the API's own host, never to a CDN redirect. Files land in the standard hub cache layout (`~/.cache/huggingface/hub/models--org--name/snapshots/<commit>/`, or `HF_HOME` / `HF_HUB_CACHE`), and the server is started with `--model <that directory>` so it can never fetch something else.

## 4. What each agent can use (the honest matrix)

From the Stage 0 research, unchanged by this build:

| | Ollama | mlx-lm |
|---|---|---|
| Codex | **supported**: `rigfile models run codex` → `codex --oss --local-provider ollama -m <model>` (documented flags). Only Ollama's default port: how Codex would be pointed elsewhere was **not verified**, so `run` refuses another port. `--oss` together with `-m` is also unverified (a note is printed) | **needs a bridge**: Codex speaks Responses; `mlx_lm.server` offers chat completions only. No bridge was verified, so it is not wired |
| Claude Code | **experimental**: `rigfile models run claude` sets `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN=ollama` for that one process, passes `--model`, and removes `ANTHROPIC_API_KEY` from its environment. Ollama documents this; Anthropic does **not** support routing Claude Code to non-Claude models, and Ollama lists gaps (`tool_choice`, deferred tools, hosted web search) | **needs a bridge**: no Anthropic Messages endpoint; not wired |
| OpenAI-compatible tools (Aider, OpenCode, Continue, Zed) and your own scripts and hooks | `rigfile models url <name>` prints the base URL | same |
| Cursor | not configured (limited support, unverified) | same |

**No global agent configuration is written**, and `gateways:` / `routing:` stay parsed and merged but are **not applied** (the plan screen says so). Per-subagent scoping of a local model for Claude Code is unverified and not attempted. `mlx_lm.server` treats the request model name `default_model` as the model it was started with (checked in its `server.py`); any other name would make it try to load that model, so `doctor` sends `default_model` (`TestMLXRequestsUseTheDefaultModel`).

## 5. Commands

`rigfile models list` (what the catalog would choose here), `pull [<rig>]`, `status`, `serve <name>`, `url <name>`, `run codex|claude`, `rm <name>`. `plan` and `apply` show a **MODELS** section (variant, download size, publisher, license, pinned revision, where it is served, per-agent wiring, what was considered and why not) and `apply --models now|later|skip`: `now` downloads and sets up, `later` (the default with `--yes`, and the answer when you decline) downloads nothing and says how to do it later, `skip` also leaves out the engine install. What was set up is recorded in `<state>/models.json` for `status`, `serve`, `url`, `run` and `rm`.

The engine is an ordinary tool: `ollama` (brew, winget, pacman: verified names) or `mlx-lm==<pinned>` through `uv tool install`, planned in the TOOLS section and run only after approval.

## 6. The model server as a service

`internal/svc` generates a per-user service, generic over name, arguments and environment: a launchd agent, a `systemd --user` unit, or a scheduled task at logon (with a `cmd` wrapper only when the task needs environment variables; values that could reach `cmd` as metacharacters are refused). Definitions are bytes written through the journaled writer (`rigfile rollback` restores them) and activated through an injectable activator; a failed activation rolls the file back. `rigd` uses the same generator, and its golden files did not change. For Ollama the unit sets `OLLAMA_HOST=127.0.0.1:<port>`; for mlx-lm it runs `mlx_lm.server --model <snapshot> --host 127.0.0.1 --port <port> ...`. Without `serve.autostart`, Ollama runs only while a model is being pulled, and `rigfile models serve` runs a server in the foreground.

## 7. `doctor`, capture, and honest expectations

For each model set up: **server** (answers), **loopback only** (the port does not accept connections on any of this machine's non-loopback addresses; a failure), **chat** (a completion returns), **tool calls** (a tool-call smoke test; if none comes back the line says CHAT ONLY: use it for summaries and commit messages, not agentic work). `rigfile init` notices an Ollama answering on `127.0.0.1:11434` and captures its models as a `models:` block pinned by digest; only the network port is read. mlx-lm servers are not captured (the revision cannot be known).

An 8B 4-bit model is fine for summaries, commit messages, simple edits and classification and far weaker than a frontier model at multi-step coding and tool use. The plan screen says so.

## 8. Tests

`internal/platform`: hardware detection per OS from fixtures. `internal/models`: `TestTheRealCatalogParsesAndChoosesPerMachine`, `TestMatchesFailsClosed`, `TestValidateRefusesWhatIsNotSafeOrPinned`, `TestResolve*`, `TestOllamaIsTheOnlyEngineWiredToTheAgents`, the `TestFetch*` group (tampering, unsafe repositories, resume, pagination, token scoping), `TestSetup*`, `TestCheckModel*`, `TestLoopbackOnlyCheckFlagsAnExposedServer`, `TestDetectOllamaAndTheCapturedSection`. `internal/svc`: units, refusals, install/start/stop/uninstall/rollback for all three OSes. `cmd/rigfile`: `TestPlanShowsTheModelsSection`, `TestApplyModelsNowPullsChecksAndRecords`, `TestOllamaDigestMismatchIsRefusedAndNotRecorded`, `TestModelsSkipLeavesEverythingOut`, `TestMLXModelsAreNotRunnableThroughAgents`, `TestModelsRunCodexOnTheDefaultPort`, `TestInitCapturesARunningOllamaModelPinnedByDigest`, `TestDoctorChecksEachSetUpModel`.
