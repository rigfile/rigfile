#!/bin/sh
# Refresh the embedded gitleaks rule data. Usage: scripts/update-gitleaks-rules.sh v8.30.1
# Review the resulting diff and re-run the scan tests and the corpus (recall must stay 100%) before committing.
set -eu
tag="${1:?usage: $0 <gitleaks tag, e.g. v8.30.1>}"
cd "$(dirname "$0")/../internal/scan/rules"
curl -fsSL "https://raw.githubusercontent.com/gitleaks/gitleaks/$tag/config/gitleaks.toml" -o gitleaks.toml
shasum -a 256 gitleaks.toml | awk '{print $1}' > gitleaks.toml.sha256
echo "$tag" > VERSION
echo "updated to $tag ($(cat gitleaks.toml.sha256))"
