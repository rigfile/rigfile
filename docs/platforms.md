# Platforms (macOS / Linux / Windows)

**Date checked:** 2026-09-25
**Origin:** `RIGFILE_PLAN.md` §9.4, corrected against the research in `docs/targets/claude-code.md` and `docs/targets/codex.md`.
**Rule:** every row carries a status.

- ✅ **verified**: confirmed in an official/public source (linked)
- ⚠️ **corrected**: the plan said something different; the correction is stated
- ❓ **unverified**: carried over from the plan (or docs are silent). Needs a test before anything depends on it. Nothing here is guessed.

All OS differences must live in one platform module (CLAUDE.md rule 5). This document is the spec for that module. It is language-neutral (ADR 0001 not yet decided).

---

## 1. Platform table

| Concern | macOS | Linux | Windows | Status |
|---|---|---|---|---|
| **Home** | `$HOME` | `$HOME` | `%USERPROFILE%` | ✅ both target tools use `~/.claude`, `~/.codex`; Claude Code docs: "On Windows, `~/.claude` means `%USERPROFILE%\.claude`" (setup/settings). Codex's Windows expansion of `~/.codex` is ❓ (the docs say `~\work` works as a home-relative path on native Windows). |
| **Where AI tools keep config** | `~/.claude`, `~/.codex`, `~/.agents/skills` | same | `%USERPROFILE%\.claude`, `%USERPROFILE%\.codex` (❓ for Codex), `%USERPROFILE%\.agents\skills` (❓) | ⚠️ **Not `${CONFIG_DIR}`.** Both CLIs use home-dot-folders on every OS, including Windows. `${CONFIG_DIR}` / `${APP_DATA}` matter only for Rigfile's own files and for GUI apps (Claude Desktop). |
| **Config dir (`${CONFIG_DIR}`) for Rigfile's own files** | `~/.config/rigfile` (CLI convention) | `$XDG_CONFIG_HOME/rigfile` or `~/.config/rigfile` | `%APPDATA%\rigfile` | ❓ plan §8.1/§9.4; a Rigfile design choice, not a vendor fact |
| **Rigfile state** | `~/.rigfile/` | `$XDG_STATE_HOME/rigfile` or `~/.rigfile/` | `%LOCALAPPDATA%\rigfile\` | ❓ design choice. Open question: pick one convention; the plan uses `~/.rigfile/` on macOS but `~/.config` for config: two conventions on one OS. |
| **Claude Code managed (system) dir** | `/Library/Application Support/ClaudeCode/` | `/etc/claude-code/` | `C:\Program Files\ClaudeCode\` (+ registry `HKLM\SOFTWARE\Policies\ClaudeCode`) | ✅ [managed-settings](https://code.claude.com/docs/en/managed-settings) |
| **Codex system dir** | `/etc/codex/` | `/etc/codex/` | `%ProgramData%\OpenAI\Codex\requirements.toml` (Windows system `config.toml` path ❓ not documented) | ✅ / ❓ [managed-configuration](https://learn.chatgpt.com/docs/enterprise/managed-configuration) |
| **Claude Desktop config** | `~/Library/Application Support/Claude/claude_desktop_config.json` | ❓ no official Linux build documented in the source checked: "Claude Desktop is available for macOS and Windows" | `%APPDATA%\Claude\claude_desktop_config.json` | ✅ macOS, Windows ([MCP quickstart](https://modelcontextprotocol.io/quickstart/user), 2026-09-25); Linux ❓ (plan said "verify official support" → **not documented; treat as unavailable until proven**). Note: Claude Desktop launches MCP servers *without* inheriting all env; on Windows the doc says to add `APPDATA` to the server's `env` if a path contains `${APPDATA}`. |
| **Secret store** | Keychain | Secret Service (libsecret) | Credential Manager | ✅ supported by `zalando/go-keyring` (macOS `/usr/bin/security`, Linux Secret Service via D-Bus, Windows) and `jaraco/keyring` (adds KWallet). Both checked 2026-09-25. |
| **Headless secret fallback** | n/a (Keychain locked over SSH: Claude Code itself falls back to a `0600` file) | encrypted file (`age`) / password manager | n/a | ❓ plan §7.2. Fact: `go-keyring` requires a Secret Service provider and a `login` collection; without one it fails. Precedent: Claude Code and Codex both fall back to plaintext `0600`/profile-ACL files (`~/.claude/.credentials.json`, `~/.codex/auth.json`). |
| **Package managers** | Homebrew | apt, dnf, pacman, zypper; Homebrew-on-Linux optional | winget (default), Scoop, Chocolatey | ✅ brew/scoop/winget/arch/fedora/debian names checked for the catalog tools (see `catalog/tools.yaml`); zypper/choco ❓ |
| **Cross-platform installers** | npm, pipx, uv, cargo, go install | same | same | ❓ plan |
| **Vendor CLI install** | `brew install --cask claude-code`; `curl -fsSL https://claude.ai/install.sh \| bash`; Codex `curl -fsSL https://chatgpt.com/codex/install.sh \| sh` | apt/dnf/apk repos or the same scripts | `winget install Anthropic.ClaudeCode`; `irm https://claude.ai/install.ps1 \| iex`; Codex `irm https://chatgpt.com/codex/install.ps1 \| iex` | ✅ [Claude setup](https://code.claude.com/docs/en/setup), [Codex CLI](https://learn.chatgpt.com/docs/codex/cli). Note: brew/winget installs of Claude Code do **not** auto-update. |
| **Background service (Stage 7)** | launchd LaunchAgent | `systemd --user` (fallback: background process) | per-user scheduled task / service | ❓ plan, Stage 7 not yet researched |
| **Shell for hooks** | sh/zsh | sh/bash | see §2 | ⚠️ corrected in §2 |
| **Open browser for login** | `open` | `xdg-open` (fallback: print URL; headless → device flow) | `start` / `rundll32 url.dll` | ❓ plan |
| **File permissions** | POSIX `0600` | POSIX `0600` | user-only ACL | ✅ pattern: Claude Code stores `.credentials.json` `0600` on macOS/Linux and relies on profile-dir ACL on Windows |
| **Path quirks** | case-insensitive (APFS default) | case-sensitive | case-insensitive, `\` separators, 260-char limit, CRLF | ❓ plan. One verified addition: Claude Code **normalizes Windows paths to POSIX form** before matching permission rules (`C:\Users\a` → `/c/Users/a`). |
| **Admin rights** | never implicit | never implicit | never implicit; UAC only with explicit confirm | ✅ consistent with vendors: writing to a managed dir needs admin; Codex's *own* `elevated` Windows sandbox setup shows a UAC prompt (Rigfile must not trigger it silently). |

---

## 2. Shells, hooks and command launching (corrected)

| Topic | Fact | Source |
|---|---|---|
| Claude Code shell on Windows | **Git Bash** if Git for Windows is installed; otherwise the **PowerShell tool**. Git for Windows is optional. | [setup](https://code.claude.com/docs/en/setup) ✅ |
| Claude Code hook launching | *Shell form* (no `args`): `sh -c` on macOS/Linux; Git Bash on Windows; PowerShell if no Git Bash; `shell: bash|powershell` overrides. *Exec form* (`args` set): spawned directly on every OS, **no shell**; on Windows `command` must be a real `.exe`, so `.cmd`/`.bat` shims (npm, npx) fail. | [hooks](https://code.claude.com/docs/en/hooks) ✅ |
| Codex hook launching | `command` (+ optional `command_windows` override). Shell semantics not spelled out in the pages read. | [hooks](https://learn.chatgpt.com/docs/hooks) ✅ / ❓ shell |
| **Consequence** | A hook implemented as `rigfile hook <name>` (exec form, native binary) behaves identically on all three OSes and needs neither bash nor PowerShell. This validates plan §6.2 "prefer `builtin:` hooks". | derived |
| Stdio MCP on Windows: `cmd /c npx …` | ❓ **Not documented** by Claude Code, Codex, or the MCP quickstart. The MCP quickstart's official Windows example uses `"command": "npx"` directly (with an `APPDATA` env workaround). The plan's claim (§9.2) may still be true for specific clients; it needs a test on a clean Windows VM before the Windows adapter hardcodes it. | [MCP quickstart](https://modelcontextprotocol.io/quickstart/user) |
| Git hooks on Windows | ❓ plan §8.1b: Git for Windows runs hooks through its bundled `sh`; GitHub Desktop / VS Code / JetBrains behavior must be tested (Stage 2). Not researched in Stage 0. | plan |

---

## 3. Sandboxing capability by OS (affects base-secure honesty)

| | macOS | Linux | Windows native | WSL2 |
|---|---|---|---|---|
| Claude Code sandbox (OS-level file/network confinement of Bash/PowerShell) | ✅ | ✅ | ❌ not supported | ✅ (WSL1 ❌) |
| Codex sandbox | Seatbelt ✅ | bubblewrap + seccomp (+Landlock fallback) ✅ | native: `elevated` (strongest, needs admin approval once) / `unelevated` (weaker; cannot enforce every carve-out; Codex refuses unsupported policies) ✅ | Linux sandbox ✅ (WSL1 ❌ since Codex 0.115) |

Sources: [Claude sandboxing](https://code.claude.com/docs/en/sandboxing), [Codex permissions § How enforcement works](https://learn.chatgpt.com/docs/permissions#how-enforcement-works), [Codex WSL](https://learn.chatgpt.com/docs/windows/wsl), checked 2026-09-25.

**Corrections to plan claims this affects**
- §8.2 assumes read-deny rules protect secrets. On **native Windows with Claude Code** there is no OS-level backstop: a script that opens `~/.ssh/id_ed25519` itself is not covered by permission rules. The plan screen must show "file-read protection is best-effort on this OS/tool".
- §8.2's "WSL: also deny the Windows side via `/mnt/c/Users/*/.ssh/**`": ❓ untested; Claude Code's Read rules use gitignore syntax with `//` absolute anchors, so it would be written as `Read(//mnt/c/Users/*/.ssh/**)`. Verify the glob semantics for `*` in a directory segment before shipping.

---

## 4. Deny-list expansion: one canonical entry, three *syntaxes*

Plan §8.2: "Deny list entries are written once in canonical form and expanded per OS". Research shows the expansion must also differ **by target tool**, not only by OS:

| Canonical (Rigfile) | Claude Code `permissions.deny` | Codex permission profile `filesystem` |
|---|---|---|
| `home:.ssh/**` | `Read(~/.ssh/**)` (macOS/Linux/Windows: `~/` is portable) | `"~/.ssh" = "deny"` (Windows may also write `~\.ssh`) |
| `home:.aws/**` | `Read(~/.aws/**)` | `"~/.aws" = "deny"` |
| `home:.claude/.credentials.json` | `Read(~/.claude/.credentials.json)` | n/a (not Codex's) |
| `home:.codex/auth.json` | `Read(~/.codex/auth.json)` | `"~/.codex/auth.json" = "deny"` |
| `abs:/etc/ssl/private/**` | `Read(//etc/ssl/private/**)` (`//` = filesystem root) | `"/etc/ssl/private" = "deny"` |
| `anywhere:**/.env` | `Read(**/.env)` | `":workspace_roots"."**/.env" = "deny"` (+ `glob_scan_max_depth`) |
| Windows drive-wide `.env` | `Read(//c/**/.env)` or `Read(//**/.env)` | absolute drive path form ❓ |

Traps found:
1. Claude Code `/path` is **not** absolute: it is relative to the settings source (`~/.claude/path` in user settings). Emitting `Read(/etc/ssl/private/**)` into user settings silently protects the wrong place. Always emit `~/` or `//`.
2. Claude Code normalizes Windows paths to `/c/Users/...` before matching; canonical Windows denies must go through the same normalization or they never match.
3. Codex deny-read only constrains sandboxed shell commands. MCP servers and connectors are outside it; a canonical deny does not cover them.

---

## 5. WSL

Detected as `linux` + `wsl=true` via `WSL_DISTRO_NAME` (✅ referenced in [Codex WSL doc](https://learn.chatgpt.com/docs/windows/wsl)). Inside WSL2 both tools behave as Linux tools (Claude Code reads `/etc/claude-code`; Codex uses the Linux sandbox). Claude Code can be told to also read the Windows policy chain with `wslInheritsWindowsSettings` (managed setting). Claude Code's "import MCP servers from Claude Desktop" works on macOS and WSL only. Everything else in plan §9.4 "WSL" is ❓.

---

## 6. Headless / remote Linux

❓ plan §9.4. Additional verified facts: Claude Code stores its login as a `0600` file on Linux (no keyring dependency); Codex's `cli_auth_credentials_store` can be `file | keyring | auto | ephemeral`, where headless behavior of `auto` is ❓. Neither tool needs a browser on the same host for every login path, but device-code behavior was not researched.

---

## 7. Corrections to the plan (summary)

1. `${CONFIG_DIR}` is **not** where Claude Code/Codex keep config; they use home-dot-folders on all OSes. (§8.1, §9.4)
2. Claude Desktop has **no documented Linux build**; only macOS and Windows paths are documented. (§9.2, §9.4)
3. `cmd /c npx` for stdio MCP on Windows is **not confirmed** by any source read; the official Windows example uses `npx` directly. It is confirmed only for *hooks*, and there in the form "exec-form hooks can't launch `.cmd` shims". (§9.2)
4. Deny-list expansion differs by **tool syntax**, not just OS (§4 above). (§8.2)
5. Sandbox availability differs: none on native Windows for Claude Code. (§8.2, §11)
6. Both tools store credentials in plaintext files on some OS/config combinations; add these to the deny list. (§8.2)
7. Rigfile needs a tie-breaker between `~/.rigfile` (state) and `~/.config/rigfile` (config) on macOS: two conventions on one OS.

## 8. Windows implementation (Stage 3, S3-M8)

Built and covered by cross-compilation (`GOOS=windows go vet ./...`), injected-GOOS unit tests on every host, and Windows-only tests that run on CI's `windows-latest` (`internal/platform`, `internal/execshim`, `internal/secrets`). **Nothing here has been run on a real Windows machine by the developer yet**: the first native run is the `windows-latest` CI job, then the clean-VM procedure in S3-M10.

| Concern | What Rigfile does | Status |
|---|---|---|
| Secret store | `go-keyring` → Windows Credential Manager (a secret is capped at 2560 bytes; a larger value is refused with a clear message) | UNVERIFIED natively |
| Secret / state files | `WritePrivate`: temp file → protected (non-inheriting) DACL granting only the current user → write → rename. `IsPrivateFile` reads the DACL and refuses anyone but the user, SYSTEM and Administrators (decision logic `ForeignTrustees` is unit-tested everywhere) | UNVERIFIED natively |
| Concurrent `secrets set` | `LockFileEx` on a sidecar `.lock` file | UNVERIFIED natively |
| Paths | `%USERPROFILE%`, `%APPDATA%` (config, Claude Desktop), `%LOCALAPPDATA%\rigfile` (state); Go adds the `\\?\` prefix for paths over MAX_PATH itself | golden files per OS |
| Line endings | marker regions and JSON edits keep an existing file's CRLF; `.gitattributes` pins LF for everything Rigfile generates or compares | tested |
| Renames | `platform.RenameReplace` retries briefly (an editor or scanner may hold the destination open) | UNVERIFIED natively |
| `rigfile exec` / MCP servers | `npx`, `npm` are `.cmd` shims: the program is resolved through PATHEXT and Go runs `.cmd` through `cmd.exe` with its own escaping, refusing arguments it cannot escape (no shell injection). Config entries call `rigfile` (a real `.exe`, which Claude Code's exec form requires), so no `cmd /c` wrapper appears in any config | Windows-only tests; `cmd /c npx` in vendor docs stays UNVERIFIED and is unneeded |
| Interrupt | Windows cannot signal a child; Ctrl-C ends the wrapped server (`platform.ForwardSignal`) | by design |
| Git hooks | Git for Windows runs the `#!/bin/sh` shims with its bundled sh; the dispatcher and `core.hooksPath` use forward-slash paths (`platform.ToShellPath`); the hooks tree is hashed without an execute bit (NTFS has none) | tested by injected paths; UNVERIFIED natively |
| PowerShell | base-secure carries `PowerShell(...)` deny/ask rules, a `powershell` guard hook and Windows credential/DPAPI read denies, all `os: [windows]` | tested |
| Tools | winget/Scoop entries in `catalog/tools.yaml` | Stage 1 |
| Reserved names | a rig whose skill/agent/command id is `con`, `nul`, `com1`... is rejected on every OS (it could not be checked out on Windows) | tested |
