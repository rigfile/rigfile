#!/bin/sh
# Runs INSIDE a real macOS VM (Tart), unprivileged, real Keychain: proves the one thing containers cannot
# (docs/local-ui.md / e2e/README.md "Things only this run can confirm"). Same shape as e2e/scenario.sh's core
# loop, without RIGFILE_PASSPHRASE_FILE, so the real macOS Keychain backend is exercised.
set -eu
fail() { echo "E2E FAIL: $*" >&2; exit 1; }
has() { grep -q -- "$1" "$2" || { echo "--- $2:" >&2; cat "$2" >&2; fail "expected '$1'"; }; }
out=/tmp/out.txt

echo "== validate";  rigfile validate /tmp/rig > $out;                   has "valid" $out
echo "== plan";      rigfile plan /tmp/rig > $out;                       has "SECRET NEEDED  demo/api_key" $out
# Found live, 2026-09-27 (real claude CLI 2.1.283): `claude mcp get <name>` -- which plan calls to show each MCP
# server's status -- initialises ~/.claude.json and backs it up to ~/.claude/backups/ on ANY invocation, even a
# nonexistent-server lookup, regardless of Rigfile. That is the real vendor CLI's own side effect, not Rigfile's:
# confirmed by running `claude mcp get` alone, with no rigfile involved at all, against a fresh $HOME (see
# docs/adr/0002-mcp-user-scope-write-path.md). What plan must still never create is Rigfile's OWN managed
# content -- the files an apply would actually write.
for f in CLAUDE.md settings.json skills agents commands; do
  [ ! -e "$HOME/.claude/$f" ] || fail "plan wrote Rigfile-managed content: ~/.claude/$f"
done
echo "== apply";     rigfile apply /tmp/rig --yes > $out 2>/tmp/err.txt; has "applied" $out
for f in CLAUDE.md settings.json skills/pdf/SKILL.md agents/reviewer.md commands/hi.md; do [ -f "$HOME/.claude/$f" ] || fail "missing ~/.claude/$f"; done
echo "== idempotent"; rigfile apply /tmp/rig --yes > $out;               has "nothing to change" $out
echo "== secrets (REAL macOS Keychain, no RIGFILE_PASSPHRASE_FILE)"
printf 'FAKE-SECRET-VALUE\n' | rigfile secrets set demo/api_key > $out 2>/tmp/err2.txt || { cat /tmp/err2.txt >&2; fail "secrets set failed"; }
has "stored" $out
security find-generic-password -s rigfile -a demo/api_key >/dev/null 2>&1 || fail "the secret is not actually in the macOS Keychain"
echo "== exec";      rigfile exec --secret DEMO_API_KEY=demo/api_key -- sh -c 'echo "key=$DEMO_API_KEY"' > $out; has "key=FAKE-SECRET-VALUE" $out
echo "== doctor (must say keychain, not the encrypted-file fallback)"
rigfile doctor > $out || { cat $out >&2; fail "doctor is not green"; }
has "macos-keychain" $out
if grep -qi "encrypted-file\|weaker" $out; then fail "doctor fell back to the file backend; the real Keychain was not used"; fi
echo "== diff/drift"; rigfile diff > $out;                               has "no drift" $out
echo "== rollback";  rigfile rollback --force > $out;                    has "rolled back" $out
[ ! -e "$HOME/.claude/skills" ] || fail "skills not rolled back"
security delete-generic-password -s rigfile -a demo/api_key >/dev/null 2>&1 || true
echo "MACOS E2E OK"
