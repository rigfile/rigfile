#!/bin/sh
# Installer E2E (needs Docker and Go): builds a Linux release for the Docker host's architecture, then runs
# scripts/install.sh inside a container against it, with the REAL minisign tool doing the signing.
set -eu
cd "$(dirname "$0")/.."
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in x86_64) goarch=amd64 ;; aarch64|arm64) goarch=arm64 ;; *) echo "unsupported docker arch $arch" >&2; exit 1 ;; esac
rm -rf e2e/build-install && mkdir -p e2e/build-install
go run ./tools/release -version 9.9.9 -out e2e/build-install/dist -targets "linux/$goarch" >/dev/null
mkdir -p e2e/build-install/ctx/release
cp e2e/build-install/dist/SHA256SUMS "e2e/build-install/dist/rigfile_9.9.9_linux_${goarch}.tar.gz" e2e/build-install/ctx/release/
cp scripts/install.sh e2e/install_scenario.sh e2e/Dockerfile.install e2e/build-install/ctx/
docker build -q -f e2e/build-install/ctx/Dockerfile.install -t rigfile-e2e-install e2e/build-install/ctx >/dev/null
docker run --rm rigfile-e2e-install
