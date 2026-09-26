#!/bin/sh
# Runs the registry tests against a throwaway Postgres in Docker.
#   scripts/registry-test.sh [go test args...]     (default: ./internal/registry/... ./cmd/rigfile-registry/...)
set -eu
cd "$(dirname "$0")/.."
name=rigfile-test-pg
docker rm -f "$name" >/dev/null 2>&1 || true
docker run -d --name "$name" -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 postgres:16-alpine >/dev/null
trap 'docker rm -f "$name" >/dev/null 2>&1 || true' EXIT INT TERM
i=0
until docker exec "$name" pg_isready -U postgres >/dev/null 2>&1; do
  i=$((i+1)); [ "$i" -lt 60 ] || { echo "postgres did not start" >&2; exit 1; }
  sleep 1
done
sleep 1
export RIGFILE_TEST_DATABASE_URL="postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable"
if [ "$#" -eq 0 ]; then set -- ./internal/registry/... ; fi
go test -count=1 "$@"
