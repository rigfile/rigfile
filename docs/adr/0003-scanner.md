# ADR 0003: Secret scanner: own matcher over embedded gitleaks rule data

**Status:** Accepted 2026-09-25 (owner chose option 1, decision O1 in `docs/stage-2-plan.md`).

## Context
base-secure needs a fast, offline, self-contained scanner for: git pre-commit/pre-push (staged blobs), agent hooks (Bash commands and Write/Edit contents), `doctor --git` (history), and later `publish` (Stage 4+). It must never echo a matched value, run in well under 100 ms on a typical commit, and be reviewable by the owner (security-sensitive code).

## Facts checked (2026-09-25)
| Question | Finding | Source |
|---|---|---|
| Licence | MIT, "Copyright (c) 2019 Zachary Rice" → vendoring rule data requires keeping the notice (`NOTICE`/`THIRD_PARTY`) | `raw.githubusercontent.com/gitleaks/gitleaks/master/LICENSE` |
| Rule format | `config/gitleaks.toml`, auto-generated ("Do not edit manually", generator `cmd/generate/config/main.go`), min gitleaks v8.25.0, **163 `[[rules]]`** in a summarising fetch of `master`; the pinned tag v8.30.1 actually has **222** (counted in the file, all compile under Go regexp). Fields: `id`, `description`, `regex`, `secretGroup`, `entropy`, `path`, `keywords` (pre-regex filter), `tags`, `allowlists` (regexes/paths/stopwords); global `[allowlist]` (35 path patterns, 10 regexes, stopwords) | `github.com/gitleaks/gitleaks` README, `config/gitleaks.toml` |
| Regex engine | Go regexp (RE2); no lookaround in the sampled rules; all rules are Go-compatible by construction (**re-verify by compiling all 163 in a test**) | same |
| Library use | Importable (`detect.NewDetector…`, `DetectString/Bytes/Source`), module `github.com/zricethezav/gitleaks/v8` v8.30.1 (2026-02-21) per pkg.go.dev, MIT. But several APIs are deprecated (`DetectFiles`, `DetectGit`, `DetectReader`, …), `Fragment`/`RemoteInfo` "will be replaced in v9": **no stability guarantee**. The canonical module path may have moved to `github.com/gitleaks/gitleaks/v8`: **UNVERIFIED** | pkg.go.dev, README |
| CLI | subcommands `git`, `dir`, `stdin`; `detect`/`protect` deprecated. Baselines via `--baseline-path`, `.gitleaksignore` fingerprints (experimental) | README |

## Options
1. **Own matcher over vendored rule data (recommended).** Embed a pinned copy of `gitleaks.toml` (recorded version + sha256, refreshed by a script, reviewed as a diff), parse with go-toml (already a dependency), evaluate keywords → regex → secretGroup → entropy → allowlists ourselves (~300 lines), add Rigfile rules (sensitive file names, size limit, our own formats). Pros: no API-stability risk, tiny, fast (keyword prefilter), we own false-positive handling, works on in-memory blobs and command text, the corpus (S2-M2) pins behaviour. Cons: we must keep semantics faithful (mitigation: run both engines over the corpus in a test-only comparison against a gitleaks binary and report differences).
2. Import gitleaks as a library. Pros: identical semantics. Cons: deprecated/unstable API, large dependency tree in a security-critical binary (violates plan §13 "minimal dependencies"), git-history sources we do not need.
3. Shell out to a `gitleaks` binary. Pros: zero code. Cons: external install required (fails "single binary on a clean machine"), process start on every commit and agent tool call, no in-memory scanning.

## Decision (proposed)
Option 1. Additions: `NOTICE` with the MIT text; `scripts/update-gitleaks-rules` that fetches a tagged release, records version and sha256, and fails CI if the embedded copy differs from its recorded hash; a test that compiles every rule with Go regexp and fails on unsupported syntax; a scan API that returns findings with rule id, path, line and a *fingerprint*, never the value.

## Consequences
- Detection quality is bounded by gitleaks' rules plus our corpus; we publish per-rule recall/FP numbers (S2-M2).
- Entropy uses gitleaks' Shannon-entropy definition (**verify against its source when implementing**).
- Rule updates are explicit, reviewable diffs.

## Implementation notes (S2-M1, 2026-09-25)
- Pinned: gitleaks **v8.30.1**, sha256 `e163e53b…329caf` (`internal/scan/rules/{gitleaks.toml,VERSION,gitleaks.toml.sha256}`); `NOTICE` carries the MIT text; `scripts/update-gitleaks-rules.sh <tag>` refreshes; `TestEmbeddedRulesMatchRecordedHash` fails on a silent edit.
- Semantics copied from gitleaks v8.30.1's detector (read from source): keyword prefilter, regex, `secretGroup` (secret re-extracted by matching the regex against the match), `entropy` (skip when `entropy <= threshold`, Shannon over runes normalised by byte length), rule and global allowlists (OR/AND `condition`, `regexTarget` secret/match/line, paths checked before content, stopwords on the secret), path-only rules, and the "generic finding dropped when a specific rule matched the same secret on that line" dedupe.
- **Deliberately different:** inline `gitleaks:allow` is OFF by default (an agent can write the marker itself; `.rigfile-allow` is the only suppression); commit allowlists never match (no commit context); every rule that fails to compile is an error, not skipped.
- **Not implemented yet (known recall gaps):** gitleaks' decoding pass (base64/hex/percent-encoded secrets are not found) and `requiredRules` (unused in v8.30.1's default config). Both go to the corpus in S2-M2 as measured misses; decoding is added if the recall target needs it.
- Speed: a single Aho-Corasick pass finds present keywords (replaced ~500 `strings.Contains` passes: 130 ms → 1.5 ms on 5k lines); the one expensive rule (`generic-api-key`, ~110 ms alone on a 500 KB diff) runs only on windows around its keyword hits, with an equivalence test against a full scan. 5,001 dense lines with a hit: ~5 ms.
