#!/bin/sh
# Runs the real-macOS e2e checks in a throwaway Tart VM: the one thing containers cannot prove (the real
# Keychain, and the real `claude` CLI's actual mcp add-json/get/remove behaviour — docs/adr/0002). Owner-run,
# not in CI (no macOS runner). Needs Tart (https://tart.run — installed here 2026-09-27 from the notarized
# GitHub release, the Homebrew tap's formula being broken at the time) and Go.
#
# Usage: e2e/run-macos.sh [--keep]   (--keep leaves the cloned VM running for manual poking; default: deleted)
set -eu
cd "$(dirname "$0")/.."
KEEP=false
[ "${1:-}" = "--keep" ] && KEEP=true

TART=/Applications/tart.app/Contents/MacOS/tart
command -v "$TART" >/dev/null 2>&1 || TART=tart
VM="rigfile-e2e-macos-$$"
BASE=ghcr.io/cirruslabs/macos-sequoia-base:latest
SHARE_DIR="$(mktemp -d)"

cleanup() {
  if ! $KEEP; then
    "$TART" stop "$VM" >/dev/null 2>&1 || true
    "$TART" delete "$VM" >/dev/null 2>&1 || true
  fi
  rm -rf "$SHARE_DIR"
}
trap cleanup EXIT

echo "=== staging the share (rigfile, the real claude CLI if found, the rig, the scripts) ==="
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$SHARE_DIR/rigfile" ./cmd/rigfile
cp -R e2e/rig "$SHARE_DIR/rig"
cp e2e/macos-scenario.sh e2e/adr0002-smoke.sh "$SHARE_DIR/"
chmod +x "$SHARE_DIR/macos-scenario.sh" "$SHARE_DIR/adr0002-smoke.sh"
if command -v claude >/dev/null 2>&1; then
  cp "$(command -v claude)" "$SHARE_DIR/claude"
  chmod +x "$SHARE_DIR/claude"
  echo "  including the real claude CLI: will also run adr0002-smoke.sh"
else
  echo "  no claude CLI on this machine's PATH: skipping the ADR 0002 smoke test, running macos-scenario.sh only"
fi

echo "=== pulling the base image (first run only; ~25GB, cached after) ==="
"$TART" pull "$BASE"

echo "=== cloning ==="
"$TART" clone "$BASE" "$VM"

echo "=== booting headless ==="
"$TART" run --no-graphics --dir="share:$SHARE_DIR" "$VM" >/dev/null 2>&1 &

echo "=== waiting for an IP ==="
i=0
while [ $i -lt 60 ]; do
  IP=$("$TART" ip "$VM" 2>/dev/null || true)
  [ -n "$IP" ] && break
  i=$((i + 1)); sleep 5
done
[ -n "${IP:-}" ] || { echo "no IP after 5 minutes" >&2; exit 1; }

echo "=== waiting for the guest agent ==="
i=0
until "$TART" exec "$VM" true >/dev/null 2>&1; do
  i=$((i + 1))
  [ $i -lt 30 ] || { echo "guest agent did not come up after 5 minutes" >&2; exit 1; }
  sleep 10
done

GUEST_HOME=$("$TART" exec "$VM" sh -c 'echo $HOME')
SHARE="/Volumes/My Shared Files/share"
# the virtiofs share can lag on first read inside the guest; pipe the scripts in directly rather than trust it
cat "$SHARE_DIR/macos-scenario.sh" | "$TART" exec -i "$VM" sh -c 'cat > /tmp/macos-scenario.sh && chmod +x /tmp/macos-scenario.sh'
cat "$SHARE_DIR/adr0002-smoke.sh" | "$TART" exec -i "$VM" sh -c 'cat > /tmp/adr0002-smoke.sh && chmod +x /tmp/adr0002-smoke.sh'
"$TART" exec "$VM" chmod +x "$SHARE/rigfile"

echo "=== running the macOS scenario (real Keychain) ==="
"$TART" exec "$VM" env "HOME=$GUEST_HOME" PATH="$SHARE:/usr/bin:/bin:/usr/sbin:/sbin" \
  sh -c "rm -rf /tmp/rig && cp -R '$SHARE/rig' /tmp/rig && /tmp/macos-scenario.sh"

if [ -f "$SHARE_DIR/claude" ]; then
  echo "=== running the ADR 0002 smoke test against the real claude CLI ==="
  "$TART" exec "$VM" env "HOME=$GUEST_HOME" PATH="$SHARE:/usr/bin:/bin:/usr/sbin:/sbin" /tmp/adr0002-smoke.sh
fi

echo "=== MACOS E2E ALL DONE ==="
