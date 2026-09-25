#!/bin/sh
# Runs INSIDE a fresh container as an unprivileged user: fresh machine -> apply -> doctor green.
set -eu
fail() { echo "E2E FAIL: $*" >&2; exit 1; }
has() { grep -q -- "$1" "$2" || { echo "--- $2:" >&2; cat "$2" >&2; fail "expected '$1'"; }; }
out=/tmp/out.txt
export RIGFILE_PASSPHRASE_FILE="$HOME/.pass"
printf 'correct horse battery staple\n' > "$RIGFILE_PASSPHRASE_FILE"; chmod 600 "$RIGFILE_PASSPHRASE_FILE"

echo "== validate";  rigfile validate /rig > $out;                       has "valid" $out
echo "== plan";      rigfile plan /rig > $out;                           has "SECRET NEEDED  demo/api_key" $out; has "TOOLS" $out
[ ! -e "$HOME/.claude" ] || fail "plan wrote to ~/.claude"
echo "== apply";     rigfile apply /rig --yes > $out 2> /tmp/err.txt;    has "applied" $out
for f in CLAUDE.md settings.json skills/pdf/SKILL.md agents/reviewer.md commands/hi.md; do [ -f "$HOME/.claude/$f" ] || fail "missing ~/.claude/$f"; done
[ -f "$HOME/.fake-claude-mcp/demo.json" ] || fail "MCP server not registered"
grep -q "DEMO_API_KEY=demo/api_key" "$HOME/.fake-claude-mcp/demo.json" || fail "MCP entry is not wrapped with rigfile exec"
grep -q "sk-\|FAKE-SECRET" "$HOME/.fake-claude-mcp/demo.json" && fail "secret value in MCP entry"
echo "== idempotent"; rigfile apply /rig --yes > $out;                  has "nothing to change" $out
echo "== secrets";   printf 'FAKE-SECRET-VALUE\n' | rigfile secrets set demo/api_key > $out 2>/dev/null; has "stored" $out
echo "== exec";      rigfile exec --secret DEMO_API_KEY=demo/api_key -- sh -c 'echo "key=$DEMO_API_KEY"' > $out; has "key=FAKE-SECRET-VALUE" $out
echo "== doctor";    rigfile doctor > $out || { cat $out >&2; fail "doctor is not green"; }
echo "== diff/drift"; rigfile diff > $out;                              has "no drift" $out
echo hand-edit > "$HOME/.claude/agents/reviewer.md"
rigfile diff > $out && fail "diff should report drift" || true;         has "agent" $out
echo "== rollback";  rigfile rollback --force > $out;                   has "rolled back" $out
[ ! -e "$HOME/.claude/skills" ] || fail "skills not rolled back"
echo "E2E OK"
