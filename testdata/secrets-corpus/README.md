# Secrets corpus

The scanner's test corpus is **generated, not stored**. `internal/scan/corpus` assembles every positive at
run time from a public prefix plus a deterministic pseudo-random body, so:

- no complete credential ever exists in this repo (the global git hook and CI gitleaks stay quiet, and no
  real secret can be mistaken for a fixture);
- the corpus is reproducible (`corpus.All(seed)`), and grows by adding a family in one place.

Measured by `TestCorpusRecallAndFalsePositives` (`internal/scan/corpus_test.go`), which writes
[`docs/scanner-metrics.md`](../../docs/scanner-metrics.md) (`go test ./internal/scan -run TestCorpus -update`)
and fails if that report is stale, if any **core** positive is missed (100% recall gate), or if hard-negative
false positives exceed **1%** (owner decision O2) or occur at all in the `.env.example` class.

Adding a positive family: add it to `positiveFamilies()`; if it is missed, fix the scanner or add a rule to
`internal/scan/rules/rigfile.toml`, never weaken the gate. Adding a negative: `negativeFamilies()`. Keep
negatives *hard* (secret-shaped, keyword-adjacent); an easy negative proves nothing.
