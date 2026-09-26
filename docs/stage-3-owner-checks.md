# Stage 3 owner checks (S3-M10)

Everything below needs a machine, an account or a network I do not have. Nothing was run against a real vendor tool, a real Windows machine or a clean macOS VM during the build. Tick items off in `docs/STATUS.md`; any surprise becomes a fix on `stage-3`, and the matching **UNVERIFIED** in `docs/targets/*.md` becomes a dated fact.

## 1. Push and read CI (10 minutes)

`git push -u origin stage-3` (Stage 2 first, if it is not pushed: `stage-3` is built on top of it). CI runs:

| Job | What it proves | Expected first-run risk |
|---|---|---|
| `test (ubuntu-latest)`, `test (macos-latest)` | full suite with `-race`, scanner timing gate | low |
| `test (windows-latest)` | the full suite natively on Windows: DACL `WritePrivate`/`IsPrivateFile`, `LockFileEx`, `.cmd` shim launching, per-OS golden files | **medium: this is the first native Windows run.** Expect one or two rounds of fixes (path separators in older tests, `git` behaviour under autocrlf, timing). Send me the log. |
| `windows-build` | cross-compiles and vets for Windows | low |
| `e2e` | Ubuntu and Fedora containers, now including Codex/Gemini CLI/Cursor and `init --from` | low: passes locally |
| `dogfood`, `secret-scan` | scanner on this repo's history | low |

## 2. Windows 11 clean-VM procedure

Use a throwaway VM (Windows 11 Dev Environment from Microsoft, Hyper-V, UTM or a cloud VM). Standard user, not administrator.

```powershell
winget install GoLang.Go Git.Git          # Git for Windows: needed for the hook shims
git clone <repo> ; cd rigfile ; git checkout stage-3
go build -o $env:LOCALAPPDATA\rigfile\rigfile.exe .\cmd\rigfile
$env:PATH += ";$env:LOCALAPPDATA\rigfile"
rigfile validate e2e\rig ; rigfile plan e2e\rig --no-git
rigfile secrets set demo/api_key          # Credential Manager: check it appears under Control Panel > Credential Manager > Generic
icacls "$env:LOCALAPPDATA\rigfile" /T      # expect only your user (+ SYSTEM, Administrators); nothing for Users or Everyone
rigfile apply e2e\rig                     # review, approve; then:
rigfile doctor                            # expect: secret store = windows-credential-manager, no warnings
rigfile diff                              # expect: no drift
git init t ; cd t ; "x" > f ; git add f ; git commit -m ok          # the hooks run through Git for Windows sh
"token = 'ghp_" + ("A1b2C3d4E5"*4).Substring(0,36) + "'" > leak.py ; git add leak.py ; git commit -m leak   # expect: blocked (fake value)
git commit --no-verify -m sneaky          # expect: blocked by the reference-transaction backstop
rigfile rollback --force
```

What only this run can settle (record each in `docs/platforms.md` §8 with the date):

- Credential Manager set/get/delete and the 2560-byte refusal message;
- the DACL on `secrets.age`, `state.json` and the backups (`IsPrivateFile` green, `icacls` clean);
- two concurrent `rigfile secrets set` (PowerShell `Start-Job`) lose no update;
- `rigfile exec -- npx --version` runs the `.cmd` shim, and `npx` with an argument like `"a & calc"` never starts a second command;
- hooks: `core.hooksPath` written with forward slashes, `git commit` works from Git Bash, PowerShell and a GUI client, and a hook that fails blocks;
- the Claude Code PowerShell tool: the `PowerShell(...)` deny/ask rules fire (try `Get-ChildItem env:`), and the `powershell` guard hook receives the payload;
- claude.exe / `codex` / `cursor` present: `rigfile plan` says "configured" and applies to `%USERPROFILE%\.claude`, `.codex`, `.cursor`, and Claude Desktop's `%APPDATA%\Claude\claude_desktop_config.json` (or the Microsoft Store path: see below).

## 3. macOS clean VM

Unchanged: the Tart procedure in `e2e/README.md`, plus `rigfile apply` with `~/.codex`, `~/.gemini`, `~/.cursor` and Claude Desktop installed, then `rigfile doctor`.

## 4. WSL2

In a WSL2 Ubuntu: `rigfile doctor` prints `linux/... (WSL)`; `rigfile plan` lists the `+ deny Read(//mnt/c/Users/*/...)` rules and the note about the Windows-side install. Then in Claude Code (base-secure applied): ask it to `cat /mnt/c/Users/<you>/.ssh/id_ed25519` (use an empty fake file). **Settles:** whether `*` in that directory segment matches your Windows user-name directory, and behaviour with a custom `automount.root`.

## 5. Live vendor checks (each settles one UNVERIFIED)

| # | Tool | Check | Where recorded |
|---|---|---|---|
| 1 | Codex | After `rigfile apply`, start Codex: does it load `AGENTS.md`, the `~/.agents/skills` skill, the agent TOML and `[mcp_servers.*]` (`/mcp`)? Does `~/.codex/rules/rigfile-base-secure.rules` (`prefix_rule`) block `git push --force`? Does `approval_policy`/`sandbox_mode` at the top of `config.toml` take effect? | `docs/targets/codex.md` |
| 2 | Codex | Does `CODEX_HOME` move the skills directory too? Windows path of `config.toml`? | `docs/targets/codex.md` |
| 3 | Gemini CLI | Does `~/.gemini/settings.json` `mcpServers` load, including a remote server written as `httpUrl`? Does `GEMINI.md` load? Does a `commands/*.toml` with `{{args}}` run? Settings shape for hooks and `tools.exclude` rule syntax (Rigfile writes neither yet) | `docs/targets/gemini-cli.md` |
| 4 | Cursor | Does `~/.cursor/mcp.json` load (Settings > MCP)? Windows location of the file. Whether user-level rules can be a file (Rigfile writes none) | `docs/targets/cursor.md` |
| 5 | Claude Desktop | Does `claude_desktop_config.json` load stdio servers wrapped in `rigfile exec`? The Microsoft Store install keeps its config in a different folder: does `DesktopDirFor` find it? | `docs/targets/claude-desktop.md` |
| 6 | Windows, all | Vendor docs show `cmd /c npx`; Rigfile never needs it (the server command is `rigfile`), but confirm a wrapped npx server starts under Claude Code on Windows | `docs/targets/claude-code.md` correction #4 |
| 7 | Claude Code | The Stage 2 live red-team items still open: PostToolUse `updatedToolOutput` shape, sandbox enforcement | `docs/red-team.md` Part 2 |

## 6. Exit criteria (plan §12, Stage 3)

| Criterion | Evidence today | Open |
|---|---|---|
| One rig applies to Claude Code + Codex (and Gemini CLI, Cursor, Claude Desktop) | container E2E and `cmd/rigfile/targets_test.go` (with fake vendor CLIs and real config files) | live vendor loads (§5) |
| Same rig, same result on macOS, Linux, Windows | per-OS golden files for every adapter; CI matrix incl. `windows-latest` | first native Windows run (§1, §2) |
| Round trip `capture(apply(x)) == x` | tests per adapter; documented losses in `docs/stage-3-plan.md` | none |
| base-secure per target, stated honestly | `docs/targets/matrix.md`, plan-screen wording | live enforcement (§5) |
