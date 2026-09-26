#!/bin/sh
# Registry E2E (needs Docker, Docker Compose and Go): builds the registry image, runs it with Postgres, then drives it
# with the real rigfile CLI from two separate "machines" (temp HOMEs): publish (private), publish public, pull, update.
set -eu
cd "$(dirname "$0")/.."
PORT=18080
compose="docker compose -p rigfile-e2e -f deploy/docker-compose.yml"
export RIGFILE_REGISTRY_PORT=$PORT
tmp=$(mktemp -d)
cleanup() { $compose down -v >/dev/null 2>&1 || true; rm -rf "$tmp"; }
trap cleanup EXIT INT TERM
fail() { echo "REGISTRY E2E FAIL: $*" >&2; $compose logs --tail 40 registry >&2 || true; exit 1; }
has() { grep -q -- "$1" "$2" || { echo "--- $2:" >&2; cat "$2" >&2; fail "expected '$1'"; }; }

echo "== build and start"
$compose up -d --build >/dev/null 2>&1 || fail "compose up"
i=0; until curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; do i=$((i+1)); [ "$i" -lt 60 ] || fail "the registry did not become healthy"; sleep 1; done
curl -fsS "http://127.0.0.1:$PORT/healthz" | grep -q '"ok"' || fail "healthz"

echo "== security headers"
curl -fsSI "http://127.0.0.1:$PORT/" > "$tmp/h.txt"
has "Content-Security-Policy: default-src 'none'" "$tmp/h.txt"; has "X-Content-Type-Options: nosniff" "$tmp/h.txt"

echo "== accounts (operator tool)"
$compose exec -T registry rigfile-registry admin create-user --login jia --github-id 1001 >/dev/null || fail "create-user"
TOK=$($compose exec -T registry rigfile-registry admin token --login jia | tr -d '\r\n')
case "$TOK" in rgf_*) ;; *) fail "no token" ;; esac

go build -o "$tmp/rigfile" ./cmd/rigfile
export RIGFILE_REGISTRY="http://127.0.0.1:$PORT"
# NEVER touch the developer's OS keychain from a script: use the encrypted-file backend (needs RIGFILE_PASSPHRASE_FILE)
export RIGFILE_SECRETS_BACKEND=file
machine() { # machine NAME -> sets HOME for a fresh machine with a private passphrase file
  export HOME="$tmp/$1"; mkdir -p "$HOME"
  printf 'correct horse battery staple\n' > "$HOME/.pass"; chmod 600 "$HOME/.pass"
  export RIGFILE_PASSPHRASE_FILE="$HOME/.pass"
}
mkrig() { # mkrig DIR VERSION
  mkdir -p "$1/instructions" "$1/commands"
  printf 'apiVersion: rigfile.dev/v1\nname: jia/e2e-rig\nversion: %s\ndescription: E2E rig\ninstructions:\n  - {id: style, file: instructions/style.md}\ncommands:\n  - {path: commands/hi.md}\n' "$2" > "$1/rigfile.yaml"
  printf '# Style\n- be terse\n' > "$1/instructions/style.md"; printf 'say hi\n' > "$1/commands/hi.md"
}

echo "== publisher machine: store the token, publish privately"
machine publisher
printf '%s\n' "$TOK" | "$tmp/rigfile" secrets set registry/127_0_0_1_$PORT/token >/dev/null 2>&1 || fail "cannot store the token"
mkrig "$tmp/rig1" 1.0.0
"$tmp/rigfile" publish "$tmp/rig1" --to-registry --ack-personal > "$tmp/out.txt" 2>&1 || { cat "$tmp/out.txt" >&2; fail "publish"; }
has "published jia/e2e-rig@1.0.0" "$tmp/out.txt"; has "is private" "$tmp/out.txt"

echo "== another machine cannot see the private rig"
machine reader
if "$tmp/rigfile" pull jia/e2e-rig --plan-only --no-git > "$tmp/out.txt" 2>&1; then fail "a private rig was pulled anonymously"; fi

echo "== publish 1.0.1 publicly, pull it"
machine publisher
mkrig "$tmp/rig2" 1.0.1
"$tmp/rigfile" publish "$tmp/rig2" --to-registry --public --ack-personal > "$tmp/out.txt" 2>&1 || { cat "$tmp/out.txt" >&2; fail "public publish"; }
has "is public" "$tmp/out.txt"
machine reader
"$tmp/rigfile" pull jia/e2e-rig --plan-only --no-git > "$tmp/out.txt" 2>&1 || { cat "$tmp/out.txt" >&2; fail "pull plan"; }
has "Source: rigfile+" "$tmp/out.txt"; has "you did not write" "$tmp/out.txt"
"$tmp/rigfile" pull jia/e2e-rig --yes --no-git > "$tmp/out.txt" 2>&1 || { cat "$tmp/out.txt" >&2; fail "pull"; }
[ -f "$HOME/.claude/commands/hi.md" ] || fail "the pulled rig was not applied"

echo "== the public page renders"
curl -fsS "http://127.0.0.1:$PORT/r/jia/e2e-rig" > "$tmp/page.html"
has "jia/e2e-rig" "$tmp/page.html"; has "rigfile pull jia/e2e-rig" "$tmp/page.html"
curl -fsS "http://127.0.0.1:$PORT/v1/search?q=e2e" | grep -q '"e2e-rig"' || fail "search"

echo "== immutability and update"
machine publisher
if "$tmp/rigfile" publish "$tmp/rig2" --to-registry --ack-personal > "$tmp/out.txt" 2>&1; then fail "a version was published twice"; fi
mkrig "$tmp/rig3" 1.1.0
"$tmp/rigfile" publish "$tmp/rig3" --to-registry --ack-personal > "$tmp/out.txt" 2>&1 || fail "publish 1.1.0"
machine reader
"$tmp/rigfile" update --plan-only --no-git > "$tmp/out.txt" 2>&1 || fail "update"
has "1.1.0" "$tmp/out.txt"
echo "REGISTRY E2E OK"
