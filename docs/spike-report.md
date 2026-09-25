# Stage 1 spike report (ADR 0001 §7)

**Date:** 2026-09-25 · **Branch:** `stage-1-spike` · **Status:** built and tested; **awaiting the owner's review and go/no-go.**
**Toolchain:** Go 1.27.1 (darwin/arm64; not installed system-wide, used from a scratch directory). Nothing was run against a real `~/.claude`, `~/.codex`, macOS Keychain or global git config.

## 1. Question and short answer

ADR 0001 accepted Go *conditionally*: build a vertical slice, measure it, and let the owner review real Go code before committing. The four spike items, plus what they turned up:

| ADR §7 item | Result |
|---|---|
| `plan` for a Claude Code `settings.json` permissions merge (JSON key order preserved) | **Done.** `rigfile plan|apply claude`. Layout-preserving, add-only, idempotent, backup-first. |
| Keychain set/get on macOS + Linux, headless `age` fallback | **Done at library level, keychain via mock.** Real macOS Keychain and real Linux Secret Service **not exercised** (see §5). Age file backend fully tested. |
| `rigfile hook pre-tool-use` shim with measured latency | **Done.** ~5 ms per call on macOS (Python was 29–74 ms; PyInstaller one-file 4.6 s). |
| Marker-splice edit of a TOML file with comments | **Done.** `internal/splice`; user content byte-for-byte untouched; property-tested. |

**Recommendation: Go.** Every technical risk the ADR listed is now resolved or bounded (§4), the two Go-specific weaknesses it predicted (TOML fidelity, JSON key order) were solved *without* an extra library, and the hot path is ~10× faster than the best Python option. The open cost is the one only you can judge: whether you can review this code comfortably (§7).

## 2. What was built (≈2,800 lines of code, ≈2,100 lines of tests, 179 passing tests, race-detector clean)

| Package | Purpose | Notes |
|---|---|---|
| `schema` | embeds `schema/rigfile.v1.json` | |
| `internal/manifest` | validate `rigfile.yaml` against the schema | 33 cases ported from the Python probes; both validators agree |
| `internal/platform` | the only OS-aware package: paths, `${CONFIG_DIR}` expansion, WSL, private file writes | macOS/Linux/Windows all unit-tested from any host; Windows stubs fail closed |
| `internal/splice` | insert/replace/remove marker-delimited regions in TOML/YAML/Markdown | hash-based drift detection, CRLF-safe, cannot be forged by rig content |
| `internal/jsonedit` | add strings to a JSON array without re-serialising | preserves indentation, key order, CRLF, compact docs |
| `internal/adapters/claudecode` | canonical permissions → Claude Code rules; plan; apply | correct path anchors; drops provably shadowed allows |
| `internal/apply` | every write: backup (0600) then atomic replace | symlink-safe, no-op on identical content |
| `internal/secrets` | keychain (go-keyring) + age/scrypt file fallback | no values in errors; tamper/wrong-passphrase fail closed |
| `internal/execshim` | `rigfile exec`: secrets into the *child's* env only | allow-listed base env; unrelated parent secrets do not leak |
| `internal/hook` | PreToolUse guard (git-hook bypass, staging secrets, `curl \| sh`, secret writes) | malformed input fails closed |
| `internal/scan` | shared sensitive-filename / secret-shape rules | spike subset; gitleaks embedding is later |
| `cmd/rigfile` | `validate`, `plan`, `apply`, `hook`, `secrets`, `exec` | e2e tests in temp dirs only |

Dependencies actually linked into the release binary: **12 modules** (`age`, `hpke`, `jsonschema/v6`, `gjson` (+`match`,`pretty`), `go-keyring`, `yaml/v3`, `x/crypto`, `x/sys`, `x/term`, `x/text`). `go-toml` is used only by tests/validation of TOML results and is not in the shipped binary. Go's `go.sum` pins every module hash.

## 3. Measurements (Apple M2, macOS 26.6.2; single machine; release build `-trimpath -ldflags "-s -w"`)

| Case | Median | Notes |
|---|---:|---|
| `rigfile hook pre-tool-use`, allowed Bash call | **5.3 ms** | process-spawn floor here is 1.6 ms |
| same, denied call | 5.3 ms | |
| same, `Write` with a 240 KB body | 22.9 ms | dominated by JSON decode + secret-shape regex over the body |
| `rigfile version` | 5.4 ms | |
| (ADR §3.1) Python interpreter + `keyring`/`tomlkit` imports | 74 ms | needs Python installed |
| (ADR §3.1) PyInstaller `--onedir` / `--onefile` | 127 ms / 4 611 ms | |

One cold first launch took 557 ms (the fresh, unsigned binary on macOS); steady state is ~5 ms. Signing/notarising (Stage 4) is expected to matter for that first-run cost; not measured.

Binary sizes (real app, all deps): darwin/arm64 **4.7 MB**, linux/amd64 5.8 MB, linux/arm64 5.4 MB, windows/amd64 5.3 MB, all static and cross-built from the one Mac. **Linux and Windows run-time latency were not measured** (no such machine here).

## 4. Findings the spike surfaced

These are real defects/risks found by writing and testing the code, most of them before anything shipped:

1. **The Stage 0 schema did not compile in Go.** Go's regex engine (RE2) rejects lookaheads, which `rigPath` and `hostPath` used. Rewritten as `allOf`/`not` with plain regexes; semantics unchanged (Python: fixture 0 errors, 29/29 probes; Go: 33/33 cases). Fixed in `a8fb416`. *Lesson:* schema patterns must stay RE2-safe; the Go tests now guard that.
2. **Splice round-trip bug** (property test, 500 random files): when a file already ended in a blank line, `Remove` deleted the user's own line. Fixed by making the separator unconditional.
3. **JSON edit correctness:** a compact one-line `settings.json` received a multi-line insertion (valid but ugly). Now inserts compactly. (A test-data bug — nil slice marshalling to `null` — was also caught; the editor correctly rejects `null` where an array is expected.)
4. **Hook parsing traps:** `git commit -nm "msg"` (clustered `--no-verify`) and `git commit -m "--no-verify"` (a message that looks like a flag) both broke the first version. Flags that take values now consume their value; clusters are scanned character by character.
5. **Library API:** `jsonschema/v6`'s `LocalizedString(nil)` panics; wrapped with a per-call printer.
6. **Earlier (Stage 0 vendor research, still relevant here):** `~/.claude.json` is written by Claude Code itself, so direct JSON edits to it are unsafe: this slice deliberately touches only `settings.json`. User-scope MCP should go through `claude mcp add-json` (decision still open, `docs/targets/claude-code.md` §5).

ADR §8 open items: **#3 JSON key order — resolved** (gjson to locate + text splicing). **#1 Linux/Windows start-up — partially** (they cross-build; not measured). **#4 gitleaks as a library — not started** (belongs with base-secure). **#2 GoReleaser Scoop/Homebrew names — not started** (Stage 4).

## 5. What was NOT verified (do not assume)

- **Real macOS Keychain and real Linux Secret Service.** `secrets` is tested against go-keyring's in-memory mock and the age file. A manual, opt-in check on your Mac (adds then deletes a `rigfile` entry) is still needed; on a desktop Linux VM and a headless one for the fallback.
- **Windows:** compiles; private-file writes and the passphrase-file check are stubs that fail closed; `BaseEnvKeys` for Windows is a guess to be tested on a clean VM.
- **Linux/Windows performance** (§3).
- **Concurrency:** the age file store has no cross-process lock (last writer wins). Needed before Stage 1 ships.
- **The hook is a second layer.** Its shell tokenizer is deliberately simple and evadable by obfuscation (`eval`, encoded commands); hard denies belong in `permissions.deny`, which the adapter already writes.
- Only the **permissions** category of the Claude Code adapter exists. Instructions, skills, agents, commands, MCP servers and hooks are not built.
- No `init`, `diff`, `rollback`, `doctor`, lockfile, `state.json`, tool installer, CI matrix or VM E2E yet.

## 6. Try it (safe: temp files only)

```bash
export PATH=$PATH:<go>/bin             # Go is not installed system-wide yet: `brew install go`
go build -o /tmp/rigfile ./cmd/rigfile
S=$(mktemp -d)/settings.json
/tmp/rigfile validate testdata/fixtures/plan-example.rigfile.yaml
/tmp/rigfile plan  claude --rig testdata/fixtures/plan-example.rigfile.yaml --settings "$S"
/tmp/rigfile apply claude --rig testdata/fixtures/plan-example.rigfile.yaml --settings "$S" --yes
echo '{"tool_name":"Bash","tool_input":{"command":"git commit --no-verify"}}' | /tmp/rigfile hook pre-tool-use
```
`--settings` is deliberately required so nothing touches a real `~/.claude` by accident.

## 7. Review guide for a Python-first reader (the go/no-go input)

Suggested order, smallest and most self-contained first (line counts are code, not tests):

1. `internal/scan/scan.go` (41): two regexes; easiest way to see Go's shape.
2. `internal/splice/splice.go` (311) then its test file: **read the tests first**; they are the spec. Property test at the bottom.
3. `internal/adapters/claudecode/permissions.go` (~210): the product logic (translation, plan, shadowing).
4. `internal/hook/hook.go` (282): the security-relevant rules and the tokenizer.
5. `internal/secrets/*.go` (336) and `internal/execshim/execshim.go` (131): the secret-handling code that most needs your eyes.

Go idioms in Python terms: errors are *returned* values (`if err != nil` is `try/except`; wrapping with `%w` is `raise … from`); an `interface` is duck typing checked at compile time; `defer` is `finally`/`with`; table-driven tests are `pytest.mark.parametrize`; goroutines appear only in `execshim` (signal forwarding). There are no generics, decorators, metaclasses, or reflection-heavy code.

**Decision requested:** *Go / no-go.* Go → Stage 1 proper starts from this slice (next: remaining Claude Code categories, `init`/`diff`/`rollback`/`doctor`, state + lockfile, tools installer, CI matrix). No-go → fall back to Python `--onedir` per ADR §7; the language-neutral Stage 0 spec, the schema and the tests' scenarios all carry over.
