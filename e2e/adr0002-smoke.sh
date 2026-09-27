#!/bin/sh
# Verifies docs/adr/0002-mcp-user-scope-write-path.md's explicitly UNVERIFIED assumptions against the real
# `claude` CLI: exit status of `mcp get` for presence/absence, and that add-json/remove --scope user work.
set -eu
fail() { echo "ADR0002 FAIL: $*" >&2; exit 1; }
echo "== claude version"; claude --version

echo "== mcp get on a name that does not exist yet: expect non-zero"
if claude mcp get rigfile-adr0002-probe >/tmp/g1.txt 2>&1; then
  fail "expected a non-zero exit for a server that was never added: $(cat /tmp/g1.txt)"
fi
echo "  exit was non-zero, as ADR 0002 assumes. Output was:"; sed 's/^/  | /' /tmp/g1.txt

echo "== mcp add-json --scope user"
claude mcp add-json rigfile-adr0002-probe '{"command":"true","args":[]}' --scope user > /tmp/a1.txt 2>&1 || { cat /tmp/a1.txt >&2; fail "add-json failed"; }
echo "  exit 0. Output was:"; sed 's/^/  | /' /tmp/a1.txt

echo "== mcp get after adding: expect zero"
claude mcp get rigfile-adr0002-probe > /tmp/g2.txt 2>&1 || { cat /tmp/g2.txt >&2; fail "get failed after a successful add"; }
echo "  exit 0, as ADR 0002 assumes. Output was:"; sed 's/^/  | /' /tmp/g2.txt

echo "== mcp remove --scope user"
claude mcp remove rigfile-adr0002-probe --scope user > /tmp/r1.txt 2>&1 || { cat /tmp/r1.txt >&2; fail "remove --scope user failed (is --scope accepted on remove?)"; }
echo "  exit 0. Output was:"; sed 's/^/  | /' /tmp/r1.txt

echo "== mcp get after removing: expect non-zero again"
if claude mcp get rigfile-adr0002-probe >/tmp/g3.txt 2>&1; then
  fail "expected a non-zero exit after remove: $(cat /tmp/g3.txt)"
fi
echo "  exit was non-zero, as ADR 0002 assumes."
echo "ADR0002 SMOKE OK"
