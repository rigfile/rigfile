# Stage 3b owner checks (S3b-M7)

Stage 3b is built and tested against fakes (a fake Hugging Face, a fake Ollama, fake service managers and fake agent binaries). Nothing below was run against the real services, real weights, or a real service manager. **The plan's original exit criterion ("Codex and Claude Code, via a subagent, use the mlx-lm reference setup") is not met and cannot be as written**, for the reasons in `docs/stage-3b-plan.md`. What is met instead: the reference setup is reproduced (verified download, pinned, served as a background service, loopback-only, doctored), and the agents that *can* use a local model are wired through Ollama.

## 1. Push and read CI

`git push -u origin stage-3b`. New on all three OSes: `internal/platform` hardware detection (the real probe test reads the CI machine), `internal/svc` goldens, the model tests. Windows: the scheduled-task wrapper and `RealProbe` (PowerShell memory query, `GetDiskFreeSpaceEx`).

## 2. Decisions

| Decision | Recommendation |
|---|---|
| Re-scope the exit criterion as above | yes; a gateway to Claude Code is unsupported by Anthropic and a Responses bridge for Codex was never verified |
| Codex only on Ollama's default port | keep until someone verifies how `--oss` is pointed elsewhere (`docs/models.md` §4) |
| Claude Code through Ollama stays "experimental" | yes; say so in the README |
| Catalog stubs (32 GB Apple, NVIDIA llama.cpp) stay unapplied | yes, until researched; the 32 GB Mac gets the 16 GB model meanwhile |

## 3. Live checks I could not run

| # | Check | How |
|---|---|---|
| 1 | The reference setup on the real machine | Apple Silicon Mac with 16 GB or more: `rigfile apply <rig with role: local-coder> --models now` (downloads ~4.6 GB; **needs `uv` installed first** for `uv tool install mlx-lm==0.31.3`), then `rigfile models status`, `rigfile doctor` |
| 2 | The pinned Hugging Face download matches the real API | the run above; if the tree API's field names or the LFS oid differ from what the tests model, this is where it shows |
| 3 | `mlx_lm.server` accepts the generated command (`--model <local snapshot dir> --host --port --prompt-cache-*`) and answers `default_model` | `doctor` after (1); note that the `mlx-lm==0.31.3` release must contain the flags (Stage 0 verified them on `main`, not on that release) |
| 4 | Ollama on Linux and Windows: `rigfile apply --models now` with `ollama` installed; the id shown by `ollama list` equals `500a1f067a9f` for `qwen3:8b` (**UNVERIFIED** equivalence with the library page's id) | `ollama list` |
| 5 | The per-user service on each OS | `rigfile apply --models now` with `serve.autostart: true`; `launchctl print gui/$(id -u)/com.rigfile.model-local-coder`, `systemctl --user status rigfile-model-local-coder`, `schtasks /Query /TN rigfile-model-local-coder`; log out and in. Same **UNVERIFIED** as `rigd`: `schtasks /Create /XML` accepting the UTF-16 file |
| 6 | Codex `--oss --local-provider ollama -m qwen3:8b` works together | a real Codex |
| 7 | Claude Code against Ollama with `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN=ollama` | a real Claude Code; note what breaks |
| 8 | The tool-call smoke test on the real model | `doctor`; `qwen3:8b` may or may not return a tool call for that prompt, and the result is honest either way |
| 9 | `doctor`'s loopback check on a machine with a firewall or VPN interface | make sure a correctly bound server is not flagged |

## 4. Not built

llama.cpp and vLLM engines, the NVIDIA and 32 GB Apple catalog entries (stubs), a gateway (LiteLLM or other), routing translation (`routing:` is parsed only), per-subagent scoping for Claude Code, LM Studio, Aider/OpenCode configuration files (their URL is available through `rigfile models url`), and capturing an mlx-lm server. Each needs research first.
