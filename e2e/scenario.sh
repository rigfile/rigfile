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
echo "== base-secure (Claude Code side)"
rigfile apply /rig --yes --overwrite --no-git > $out 2>&1 || true
st="$HOME/.claude/settings.json"
grep -q 'Read(~/.ssh/\*\*)' "$st" || fail "base-secure deny rules missing from settings.json"
grep -q '"disableBypassPermissionsMode": "disable"' "$st" || fail "bypass mode is not disabled"
grep -q 'Security baseline (managed by rigfile/base-secure' "$HOME/.claude/CLAUDE.md" || fail "security baseline snippet missing"
echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"sh -c \"git commit --no-verify -m x\""}}' | rigfile hook run guard > $out; has '"permissionDecision":"deny"' $out
echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"cat ~/.ssh/id_ed25519"}}' | rigfile hook run guard > $out; has 'no-read-credentials\|credentials' $out
tokw="gh""p_""wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"
printf '{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{"stdout":"K=%s"}}' "$tokw" | rigfile hook run redact > $out; has 'REDACTED:github-pat' $out
if grep -q "$tokw" $out; then fail "redact hook echoed the secret"; fi
echo "== git protections (base-secure)"
# apply again after the rollback. --overwrite: rollback does not unregister MCP servers (known gap), so the
# server registered by the first apply would otherwise be reported as 'not managed by Rigfile'
rigfile apply /rig --yes --overwrite > $out;                                    has "applied" $out
hp="$(git config --global --get core.hooksPath)"
[ "$hp" = "$HOME/.config/rigfile/git-hooks" ] || fail "core.hooksPath is '$hp'"
grep -q '!.env.example' "$HOME/.config/git/ignore" || fail "global gitignore block missing"
export GIT_AUTHOR_NAME=dev GIT_AUTHOR_EMAIL=dev@example.test GIT_COMMITTER_NAME=dev GIT_COMMITTER_EMAIL=dev@example.test
repo=$(mktemp -d); cd "$repo"; git init -q -b main; echo hello > README.md
git add -A; git commit -q -m clean || fail "a clean commit was blocked"
# the fake token is assembled at run time so this script never contains a whole credential
tok="gh""p_""wJ4kP9xQm2Rt7VbN5cLd8HyZaE3sUfG6TiOo"
printf 'token = "%s"\n' "$tok" > leak.py; git add -A
if git commit -q -m leak 2> $out; then fail "a commit with a secret was allowed"; fi;  has "commit blocked" $out
if grep -q "$tok" $out; then fail "the hook printed the secret"; fi
before=$(git rev-parse HEAD)
if git commit -q --no-verify -m sneaky 2> $out; then fail "--no-verify let a secret commit through"; fi;  has "ref update blocked" $out
[ "$(git rev-parse HEAD)" = "$before" ] || fail "the branch moved"
git reset -q; rm leak.py
# timing on a real Linux git (informational)
s=$(date +%s%N); for i in 1 2 3 4 5; do echo $i > f$i; git add -A; git commit -q -m c$i; done; e=$(date +%s%N)
echo "  5 clean commits with hooks: $(( (e - s) / 5000000 )) ms each"
cd - > /dev/null
rigfile rollback --force > $out;                                    has "rolled back" $out
[ -z "$(git config --global --get core.hooksPath)" ] || fail "core.hooksPath should be gone after rollback"
echo "E2E OK"
