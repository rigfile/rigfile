#!/bin/sh
# Container E2E: for each distro, start from a fresh image, apply the e2e rig as an unprivileged user
# and check the result. Usage: e2e/run.sh [ubuntu|fedora ...]   (default: both)
# macOS is not covered here: see e2e/README.md for the owner-run Tart procedure.
set -eu
cd "$(dirname "$0")/.."
distros="${*:-ubuntu fedora}"
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in x86_64) goarch=amd64 ;; aarch64|arm64) goarch=arm64 ;; *) echo "unsupported docker arch $arch" >&2; exit 1 ;; esac
mkdir -p e2e/build
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -o e2e/build/rigfile ./cmd/rigfile
status=0
for d in $distros; do
  echo "=== $d ($goarch)"
  docker build -q -f "e2e/Dockerfile.$d" -t "rigfile-e2e-$d" e2e >/dev/null
  if docker run --rm "rigfile-e2e-$d"; then echo "=== $d: PASS"; else echo "=== $d: FAIL"; status=1; fi
done
exit $status
