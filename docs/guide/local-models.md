# Local models

A rig can ask for a local language model for cheap, private work (summaries, commit messages, simple subagents, a fallback when you hit a limit). Each machine picks the variant that fits its hardware; downloads are pinned and verified.

## In a rig

```yaml
models:
  local-coder:
    role: local-coder                     # resolved from Rigfile's model catalog for this machine
    purpose: [summaries, commit-messages, fallback]
    serve: { host: 127.0.0.1, port: 8080, api: openai, autostart: true }
    variants:                             # optional; the first that fits this machine wins
      - when: { os: macos, arch: arm64, min_memory_gb: 16 }
        engine: mlx-lm
        engine_version: "0.31.3"
        model: mlx-community/Qwen3-8B-4bit
        revision: 545dc4251c05440727734bcd94334791f6ab0192   # a 40-hex commit
        weights_format: mlx-safetensors
      - when: {}
        engine: ollama
        model: "qwen3:8b"
    license_ack: Apache-2.0
```

## Commands

```sh
rigfile models list                 # the catalog's roles and what this machine would get
rigfile models pull                 # download and set up the rig's models (verified, pinned)
rigfile models status               # what is set up here, and whether its server answers
rigfile models serve local-coder    # run a model's server in the foreground
rigfile models url local-coder      # its OpenAI-style base URL, for scripts and other tools
rigfile models run codex            # start Codex against an Ollama model
rigfile models rm local-coder
```

`plan`/`apply` ask what to do with a rig's models: `--models now` (download and set up), `later` (the default with `--yes`), or `skip`.

## Safety rules

- Only the **Ollama** and **mlx-lm** engines are applied. Others are accepted in the schema but never run.
- Only safe weight formats: safetensors, GGUF, MLX safetensors. Pickle formats and remote code are not expressible.
- A Hugging Face model must be pinned to a commit and an engine version. An Ollama model is pinned by digest when one is given; a mismatch is an error.
- Model servers listen on **loopback only** (`127.0.0.1`); anything else is refused.
- Weights come from their source and are never re-hosted by Rigfile.

## Which agents can use what

| | Ollama | mlx-lm |
|---|---|---|
| Codex | supported: `rigfile models run codex` (Ollama's default port only) | needs a bridge; not wired |
| Claude Code | experimental: `rigfile models run claude` points one session at Ollama. Anthropic does not support non-Claude models in Claude Code | not wired |
| OpenAI-compatible tools (Aider, OpenCode, Continue, Zed) and your scripts | `rigfile models url <name>` | `rigfile models url <name>` |

No global agent configuration is written for models, and `gateways:` / `routing:` in a manifest are parsed but not yet applied (the plan says so).
