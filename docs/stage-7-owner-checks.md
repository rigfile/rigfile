# Stage 7 owner checks (S7-M7)

Stage 7 (`rigd`, the local secret broker, "Level 2") is built and tested against local servers, real child processes and a real TLS interception path. Nothing below has been run against a real vendor API, a real MCP server, or a real service manager. The exit criterion is: **a red team on macOS, Linux and Windows in which a deliberately malicious MCP server tries to exfiltrate its key to an attacker host, and only a surrogate leaks, with the request blocked and logged.** The suite is `TestRedTeamBroker` (29 attempts, results in `docs/red-team-broker.md`); it runs in CI on all three OSes. It has passed locally on macOS only.

## 1. Push and read CI

`git push -u origin stage-7`. The new tests that matter on Windows: `TestServiceFiles`, `TestBrokerBackgroundStartServesLevel2` (a detached `rigfile broker run`, the file-backed secret store, real `exec`), `TestRedTeamBroker` (the child process loads the session CA from `SSL_CERT_FILE` itself; it is the test's stand-in for Node and Python). Send me any red log.

## 2. Decisions

| Decision | Recommendation | Where it lands |
|---|---|---|
| **Is Level 2 opt-in?** | yes, as built: `rigfile broker enable`. Make it the default only after you have run it on your own machine for a while | `rigd.Config` |
| **The residual risk: a compromised child runs as you** and can read the broker token. **Reduced (S8 follow-up)**: bindings now come from the policy `rigfile apply` approved, so the child can no longer bind a key to a host of its choosing; it can still borrow another approved server's session and spend that key at its own host (red-team row "not stopped") | accept; the remaining step is OS-level separation (broker under another account, or a keychain ACL for the token) | `docs/rigd.md` §3a and §8 |
| **Fail closed when the broker is not running** (a server that declares `network.allow` does not start) | yes, as built; the message names `broker run`, `broker install` and `broker exclude <server>` | `docs/rigd.md` §7 |
| **Package registries added to the session allowlist for `npx`/`uvx` launchers** | keep; it is what makes the common case work. The cost (a compromised server can also talk to the registry) is written down | `docs/rigd.md` §7 |
| **Which bundled rigs should declare `network.allow` and `secrets.<ref>.hosts`** | every rig that has a secret; without them the server stays at Level 1 with a notice | rig authors |

## 3. Live checks I could not run

| # | Check | How |
|---|---|---|
| 1 | `rigfile broker install` really works on each OS | macOS: `rigfile broker install`, then `launchctl print gui/$(id -u)/com.rigfile.rigd`, log out and in, `rigfile broker status`. Linux: same with `systemctl --user status rigfile-rigd`; also try a machine without a user systemd (WSL1, containers) and use `rigfile broker start`. Windows: `schtasks /Query /TN rigfile-rigd /XML`, log off and on. **UNVERIFIED:** that `schtasks /Create /XML` accepts the UTF-16 file exactly as generated |
| 2 | The service can read the secret store | on macOS the launch agent must be able to read your login keychain without a prompt; on Linux the Secret Service needs an unlocked session. If the store is the encrypted file, the service cannot prompt for the passphrase and must be run by hand |
| 3 | A **real MCP server** works through the proxy | pick one that uses a key (Alpaca, GitHub, Brave Search), declare its `network.allow` and `secrets.<ref>.hosts`, `rigfile broker enable`, `rigfile apply`, use it from Claude Code. Watch `<state>/rigd/audit.jsonl`. Expect trouble from clients that ignore `HTTPS_PROXY` or pin certificates (they need `broker exclude`) |
| 4 | Node's built-in `fetch` follows the proxy: `NODE_USE_ENV_PROXY=1` is documented for recent Node versions | run a server on the Node version you use; if `fetch` ignores it, note the version in `docs/rigd.md` §8 |
| 5 | Python (`requests`, `httpx`), `curl` and Go programs trust the session CA through the variables we set | a one-liner in each with the environment `rigfile exec` gives; Go on macOS and Windows will ignore `SSL_CERT_FILE` (documented; that is a Level 1 case) |
| 6 | Streaming responses (server-sent events) survive interception | a server that streams; the proxy flushes each chunk |
| 7 | A vendor API accepts the swapped key and that the response scrubbing does not corrupt real responses | any key-using server |

## 4. Legal and privacy

The broker keeps an audit log of hosts, paths (without queries) and decisions on the user's machine. It is never uploaded. Mention it in the privacy policy you still need only if you ever add telemetry.

## 5. External security review

Add `internal/rigd`, `cmd/rigfile/exec_level2.go` and the service files to the list for the review in `docs/registry-security.md` §5. A reviewer should first try the **✘ evades** row and then look for a *new* way past the proxy: request smuggling through the interception (HTTP/1.1 framing), a Host/target mismatch, a redirect that keeps a credential, or a way to make the proxy dial an internal address.
