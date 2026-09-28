# ADR 0001: Implementation language for the Rigfile CLI

- **Status:** **Accepted 2026-09-25** by the owner (RIGFILE_PLAN.md §17 Q3). The Stage 1 spike (§7, `docs/spike-report.md`) passed and the owner confirmed **go** on 2026-09-25; the Python fallback is no longer in play unless a later ADR reopens it.
- **Date:** 2026-09-25
- **Deciders:** the owner. Drafted by Claude Code.
- **Options:** Go, Python, TypeScript
- **Decision:** Go, with a 2–3 day spike at the start of Stage 1 that has the owner review real Go code, and an explicit fallback (Python, one-dir bundle) if that review is too painful. See §7.

---

## 1. Context

Rigfile's central promise (plan §1, §15) is *"a brand-new machine, one command, ≤ 10 minutes"* on macOS, Linux and Windows. That puts these things on the critical path:

1. The tool must run on a machine that has **nothing installed yet** (no Python, no Node, possibly no package manager configured).
2. It must talk to the **OS secret store** on three OSes (Keychain, Secret Service, Credential Manager) and fall back to an encrypted file on headless Linux.
3. It ships **git hooks and agent hooks that run on every commit / every tool call** (plan §8.1b, §8.2: `rigfile hook …`), so process start-up cost is paid constantly.
4. It edits other tools' config files (**JSON, TOML, Markdown**, YAML) without clobbering the user's content, including the hand-edited Codex `config.toml`.
5. It embeds a **secret scanner** (plan §13: gitleaks rules) and security-critical code (secrets, apply, hooks) that the owner must be able to review.
6. It needs a TUI for the plan and publish screens, and a release pipeline for three OSes (signed, notarized).

The owner is **Python-first**. The plan recommends Go. This ADR tests that recommendation against evidence gathered on 2026-09-25 rather than repeating it.

## 2. Decision drivers (in priority order)

| # | Driver | Why it ranks here |
|---|---|---|
| D1 | Runs on a clean machine with no runtime | The product's headline promise |
| D2 | Hot-path start-up latency (hooks) | Paid on every tool call and commit |
| D3 | Cross-OS secret store + headless fallback | Core security feature |
| D4 | Owner can read, review and maintain the code | One developer; security-sensitive code |
| D5 | Faithful editing of TOML/JSON/Markdown | Trust: never clobber user config |
| D6 | Release pipeline: cross-build, sign, notarize, package managers | Stage 4 |
| D7 | Embeddable scanner (gitleaks) | base-secure |
| D8 | TUI quality on all three OSes | UX |
| D9 | Supply-chain surface / reproducibility | Plan §11 "CLI itself compromised" |

## 3. Evidence

### 3.1 Measured on this machine (2026-09-25)

Apple M2, macOS 26.6.2, one run of 15 launches each (6 for the slow one), `hello`-class programs that import/link the kind of libraries the CLI would use. Toolchains were downloaded into a scratch directory; nothing was installed system-wide. Go 1.27.1, Bun 1.4.2, PyInstaller with Python 3.13.5, Node 20.19.6.

| Build | Median start-up | Size | Notes |
|---|---:|---:|---|
| `/usr/bin/true` (floor) | 2 ms | n/a | process-spawn floor |
| **Go** static binary (`CGO_ENABLED=0`) | **4 ms** | 4.3 MB | also built for linux/amd64, linux/arm64, windows/amd64 from this Mac in seconds: 4.3 / 4.3 / 4.5 MB, statically linked |
| **Bun** `--compile` | 10 ms | 62 MB (darwin), 81 MB (linux), 86 MB (windows) | cross-targets compiled on this Mac; each downloads the target runtime |
| Node 20 `-e 0` (needs Node installed) | 22 ms | n/a | for reference |
| Python interpreter, stdlib imports only | 29 ms | n/a | needs Python installed |
| Python interpreter + `keyring` + `tomlkit` imports | 74 ms | n/a | needs Python + venv |
| **PyInstaller `--onedir`** | **127 ms** | 22 MB (directory) | not a single file |
| **PyInstaller `--onefile`** | **4 611 ms** | 9.9 MB | unsigned; cause of the slowness not investigated (self-extraction on each launch, possibly macOS scanning the unsigned extracted binary) |

Caveats, stated plainly: single machine; the shell was a sandboxed tool session; hello-world programs, not the real app; Windows and Linux start-up were **not** measured; the Go binary for the real CLI (TUI, keyring, TOML/YAML, scanner) will be larger and slightly slower than the hello build. The ordering and magnitudes are what matter: an unsigned PyInstaller one-file binary is unusable in a per-tool-call hook, and a Python hook costs 30–130 ms per call versus ~4 ms for Go.

### 3.2 Facts checked from public sources (2026-09-25)

| Topic | Go | Python | TypeScript |
|---|---|---|---|
| Secret store lib | `zalando/go-keyring`: pure Go (no cgo). macOS via `/usr/bin/security` (secret passed on stdin, not argv, in `keyring_darwin.go`), Linux Secret Service over D-Bus (needs a `login` collection), Windows. Last push 2026-07-24. | `jaraco/keyring`: macOS Keychain, Secret Service, **KWallet**, Windows. Last push 2026-04-13. | `atom/node-keytar` is **archived** (2022-12-12). `@napi-rs/keyring` (Brooooooklyn/keyring-node) is maintained (2026-09-25) but is a **native addon**. |
| Native addon in a single binary | n/a | n/a | Node SEA is "Stability 1.1: active development"; loading `.node` addons from a SEA requires extracting to a temp file and `process.dlopen()`; cross-platform SEAs must disable code cache/snapshots. |
| Headless encrypted-file fallback | `FiloSottile/age` (reference implementation, BSD-3, 2026-08-29) | `woodruffw/pyrage` (MIT, 2026-09-21) | `FiloSottile/typage` (BSD-3, 2026-08-28) |
| TUI | Bubble Tea, 45k★, MIT, pushed 2026-09-24 | Textual, 37k★, MIT, pushed 2026-07-11; README lists macOS/Linux/Windows | Ink, 40k★, MIT, pushed 2026-09-21 |
| Style-preserving TOML edit | `pelletier/go-toml/v2`: marshals *with* comments and offers an `unstable` AST parser; **no documented comment-preserving round trip** | `tomlkit`: README: "style-preserving… preserves all comments, indentations, whitespace and internal element ordering" | not evaluated (`eemeli/yaml` preserves YAML comments) |
| Scanner | **gitleaks is Go/MIT**; `detect` package present in the repo (usable as a library; API stability not verified) | shell out to a gitleaks binary, or port its TOML rule data and adapt RE2-style regexes | same as Python |
| Cross-compile | built-in (`GOOS`/`GOARCH`), demonstrated above | PyInstaller is per-OS (not verified in Stage 0 whether any cross mode exists); Nuitka is AGPL-3.0 (GitHub API license field) | Bun compile cross-targets work (demonstrated) |
| Release tooling | GoReleaser (MIT, active 2026-09-21). Doc pages exist for `homebrew_casks`, `winget`, `nfpm` (deb/rpm), `sbom`, `notarize`. Pages for `brews` and `scoops` returned 404 on the fetch, so **Scoop and Homebrew-formula publishing are unconfirmed** (likely renamed; check at Stage 4). | PyInstaller (per-OS builds in CI), plus the wrapper channels the plan lists | Bun/Node SEA; per-OS |

## 4. Options analysis

### 4.1 Go
- **For:** one ~4 MB static binary per OS/arch, cross-built from a single machine (D1, D6). 4 ms start-up makes `rigfile hook …` on every tool call effectively free (D2). Pure-Go keyring and age (D3). The scanner engine the plan wants to embed is native Go (D7). Bubble Tea is mature (D8). One toolchain, small dependency tree (D9).
- **Against:** the owner is Python-first (D4). No style-preserving TOML editor in the ecosystem (D5): needs the marker-splice design below. Preserving JSON key order requires ordered decoding rather than the default `map` (needs a spike). Error handling is verbose; reviewing large diffs written by an assistant in an unfamiliar language is harder.

### 4.2 Python
- **For:** the owner's strength (D4). `tomlkit` is the best TOML editor of the three (D5). `dict` keeps insertion order for JSON. `keyring` covers KWallet as well (D3). Textual is excellent (D8). Fastest to prototype adapters and the merge logic.
- **Against:** D1/D2 are structurally hard. A Python CLI needs an interpreter on a fresh machine: options are (a) require `uv`/`pipx` first, which is the chicken-and-egg problem Rigfile exists to solve (uv is itself in `catalog/tools.yaml`); (b) a PyInstaller bundle. Measured: `--onefile` is 4.6 s per launch (unusable for hooks), `--onedir` is 127 ms and is a directory, not a file. Per-OS builds (D6). Nuitka is AGPL. The scanner must be shelled out to or ported (D7). Larger dependency tree to ship and audit (D9).

### 4.3 TypeScript
- **For:** Bun `--compile` gives a real single file that cross-builds and starts in 10 ms (D1, D2). Ink for TUI. JS objects preserve key order.
- **Against:** 62–86 MB binaries. `keytar` is archived; the maintained option is a native addon, which is awkward inside a single executable (D3). Node SEA is still "active development". Bun is younger and single-vendor for this use. Two-language pull toward the planned Next.js site is a small plus, not a driver. The owner has no stated TS preference, so it does not help D4.

### 4.4 Scores (1 = poor, 5 = strong; my judgement from §3, not a measurement)

| Driver | Go | Python | TypeScript |
|---|:-:|:-:|:-:|
| D1 clean-machine, single artifact | **5** | 2 | 4 |
| D2 hook latency | **5** | 2 | 4 |
| D3 secret stores + headless | 4 | **4** | 3 |
| D4 owner review/maintain | 2 | **5** | 3 |
| D5 faithful config editing | 3 | **5** | 3 |
| D6 release pipeline | 4 | 2 | 3 |
| D7 embeddable scanner | **5** | 2 | 2 |
| D8 TUI | 4 | 4 | 4 |
| D9 supply-chain surface | **4** | 2 | 2 |

Unweighted, Go leads. The two drivers where Python wins outright (D4, D5) are about *development*, while the drivers where Go wins (D1, D2, D6, D7) are about *what users get*. D4 is the real cost and is addressed in §6.

## 5. Decision

Adopt **Go** for the `rigfile` CLI, hooks, scanner and platform layer.

## 6. Consequences and mitigations

1. **Owner review burden (D4).** Keep the Go codebase small, boring and heavily tested: no generics-heavy abstractions, table-driven and golden-file tests (plan §9.3), structured logging. Security-sensitive packages (secrets, scan, apply, hooks) require a written threat note per PR (working agreement 7) and get the external review already planned for Stages 5–7. Ask for a "what does this do, in Python terms" summary in PR descriptions for the first few weeks.
2. **TOML/JSON fidelity (D5).** Do **not** depend on a round-tripping library. Design apply around **text splicing inside Rigfile-owned regions**: comment markers in Markdown and TOML (`# rigfile:begin …`), and key-level tracking in `state.json` for JSON (no comments). Parse only to validate and detect conflicts. This is required for Markdown anyway and makes the decision language-independent. JSON key-order preservation is a spike item.
3. **Python where it is strong.** Test-corpus generation, dev scripts and docs tooling may stay in Python. The existing untracked Python scaffold in this repo (`pyproject.toml`, `src/rigfile`, `tests/`) is not the product; decide separately whether to keep it as dev tooling or delete it.
4. **Windows and Linux start-up** (not measured here) become explicit Stage 1 exit measurements: hook latency p50/p95 on all three OSes.
5. **Release pipeline.** Confirm GoReleaser's current Homebrew/Scoop publishing at Stage 4 (see §3.2).
6. **Language-neutral spec.** Everything produced in Stage 0 (schema, catalogs, merge semantics, platform table) stays valid if this ADR is overturned.

## 7. What would change this decision

- If hooks and scanning are moved **out of the binary** (plain scripts), or start-up budget is relaxed to ~100+ ms, the balance shifts toward Python.
- If the target users can be assumed to already have `uv` (or Python), Python + `uv tool install` becomes a legitimate distribution story and D1 weakens. It contradicts plan §13 ("no runtime to install on a fresh machine").
- If, after the Stage-1 spike, the owner finds reviewing the Go code impractical, switch to **Python with a PyInstaller `--onedir` bundle** and move the hot-path hooks to a tiny separate native helper, accepting a two-language build. (Not `--onefile`, per the measurement.)
- If a maintained, comment-preserving Go TOML editor appears, D5 stops being a Go weakness (it is only a mitigation today).

### Stage-1 spike (2–3 days, before committing) — EXECUTED 2026-09-25, results in `docs/spike-report.md`; go/no-go pending the owner's review
Implement one vertical slice in Go: `plan` for a Claude Code `settings.json` permissions merge; keychain set/get on macOS + Linux (desktop keyring and headless age fallback); a `rigfile hook pre-tool-use` shim, with latency measured on macOS, Ubuntu and Windows; a marker-splice edit of a TOML file with comments. The owner reviews the diff. **Go/no-go on the ADR happens at the end of the spike.** *Outcome:* all four slices built and tested (hook ≈ 5 ms; binaries 4.7–5.8 MB on four targets; 12 linked modules); recommendation stays Go; owner review outstanding.

## 8. Open items
1. Windows and Linux start-up numbers (not measured; the spike measured macOS only: hook ≈ 5 ms; all four targets cross-build).
2. GoReleaser Scoop / Homebrew-formula pages: confirm current names and features.
3. ~~JSON key-order preservation approach in Go.~~ **Resolved:** gjson to locate + text splicing (`internal/jsonedit`), no ordered-map library.
4. `gitleaks/detect` as an importable library: API stability and licence terms for embedding.
5. Cause of the 4.6 s PyInstaller one-file start-up (informational; does not change the decision).

## 9. Sources (all fetched 2026-09-25)
- Measurements: this document §3.1 (scratch builds, not committed).
- https://github.com/zalando/go-keyring (README, `keyring_darwin.go`) · https://github.com/jaraco/keyring (README) · https://github.com/atom/node-keytar (archived) · https://github.com/Brooooooklyn/keyring-node
- https://github.com/charmbracelet/bubbletea · https://github.com/Textualize/textual · https://github.com/vadimdemedes/ink
- https://github.com/python-poetry/tomlkit (README) · https://github.com/pelletier/go-toml (README, `unstable`) · https://github.com/eemeli/yaml
- https://github.com/gitleaks/gitleaks · https://github.com/FiloSottile/age · https://github.com/woodruffw/pyrage · https://github.com/FiloSottile/typage
- https://github.com/nodejs/node/blob/main/doc/api/single-executable-applications.md
- https://github.com/pyinstaller/pyinstaller · https://github.com/Nuitka/Nuitka · https://github.com/oven-sh/bun
- https://goreleaser.com/customization/ (pages: homebrew_casks, winget, nfpm, sbom, notarize)
