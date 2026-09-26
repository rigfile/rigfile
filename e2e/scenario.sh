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
rigfile doctor > $out || { cat $out >&2; fail "doctor is not green with base-secure and git protections applied"; }
has "base-secure git" $out
rigfile rollback --force > $out;                                    has "rolled back" $out
[ -z "$(git config --global --get core.hooksPath)" ] || fail "core.hooksPath should be gone after rollback"
echo "== other targets (Codex, Gemini CLI, Cursor)"
mkdir -p "$HOME/.codex" "$HOME/.gemini" "$HOME/.cursor"
rigfile plan /rig --no-git > $out;                                              has "Targets: claude-code, codex" $out; has "gemini-cli" $out; has "cursor" $out
rigfile apply /rig --yes --overwrite --no-git > $out 2>&1 || { cat $out >&2; fail "multi-target apply failed"; }
[ -f "$HOME/.codex/config.toml" ] && [ -f "$HOME/.codex/AGENTS.md" ] && [ -d "$HOME/.agents/skills/pdf" ] || fail "Codex was not configured"
grep -q '\[mcp_servers.demo\]' "$HOME/.codex/config.toml" || fail "Codex MCP server missing"
grep -q 'approval_policy' "$HOME/.codex/config.toml" || fail "Codex base-secure defaults missing"
[ -f "$HOME/.gemini/settings.json" ] && [ -f "$HOME/.gemini/GEMINI.md" ] || fail "Gemini CLI was not configured"
[ -f "$HOME/.cursor/mcp.json" ] || fail "Cursor was not configured"
for f in "$HOME/.codex/config.toml" "$HOME/.gemini/settings.json" "$HOME/.cursor/mcp.json"; do
  grep -q 'FAKE-SECRET\|secret://' "$f" && fail "secret or unresolved reference in $f"
  grep -q 'rigfile' "$f" || fail "MCP server in $f is not wrapped with rigfile exec"
done
rigfile diff > $out;                                                            has "no drift" $out
rigfile doctor > $out || { cat $out >&2; fail "doctor is not green with several targets"; }
echo "== capture what was applied (round trip)"
rigfile init --from codex --out /tmp/captured-codex --name e2e/captured > $out || { cat $out >&2; fail "init --from codex failed"; }
[ -f /tmp/captured-codex/rigfile.yaml ] || fail "no rig was captured"
grep -q 'FAKE-SECRET' /tmp/captured-codex/rigfile.yaml && fail "captured rig contains a secret"
rigfile rollback --force > $out;                                                has "rolled back" $out
[ ! -e "$HOME/.agents/skills/pdf" ] || fail "Codex skill not rolled back"
echo "== publish a rig as a clean repository, then pull it back from git"
export GIT_AUTHOR_NAME=dev GIT_AUTHOR_EMAIL=dev@example.test GIT_COMMITTER_NAME=dev GIT_COMMITTER_EMAIL=dev@example.test
rigfile publish /rig --to-git /tmp/published > $out 2>&1 || { cat $out >&2; fail "publish failed"; }
has "scan proof: 0 finding(s)" $out
[ -f /tmp/published/README.md ] && [ -f /tmp/published/rigfile.yaml ] || fail "published repo is incomplete"
( cd /tmp/published && git init -q -b main && git add -A && git commit -q --no-verify -m "publish" ) || fail "cannot commit the published repo"
rigfile pull file:///tmp/published --plan-only --no-git > $out 2>&1 || { cat $out >&2; fail "pull --plan-only failed"; }
has "Source: file:///tmp/published" $out; has "you did not write" $out
[ ! -e "$HOME/.agents/skills/pdf" ] || fail "plan-only pull wrote to the machine"
rigfile pull file:///tmp/published --yes --overwrite --no-git > $out 2>&1 || { cat $out >&2; fail "pull failed"; }
has "applied" $out
rigfile update --plan-only --no-git > $out 2>&1;                                has "is up to date" $out
rigfile rollback --force > $out;                                                has "rolled back" $out
echo "E2E OK"
