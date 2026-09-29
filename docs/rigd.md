# `rigd`: the local secret broker, spec (S7-M0)

Status: spec of record for Stage 7. Background: RIGFILE_PLAN.md §7. This document says how a surrogate is made, what the proxy will and will not do with a request, and what an attacker who owns the MCP server can and cannot learn.

## 1. Threat model

The attacker controls the **MCP server process** (a malicious package, a compromised update) or the **agent's instructions** (prompt injection) and wants the user's API key. What the attacker has: the child's environment, its network, its files. What they must not get: the real key, or the ability to spend it on a host the user did not bind it to.

| Attack | Level 1 (`rigfile exec`) | Level 2 (`rigd`) |
|---|---|---|
| Read the key from the environment and POST it to an attacker host | key leaks | only a surrogate leaks; the request never leaves (blocked at CONNECT) and is logged |
| Send the key to an allowed host that is not the bound one | key leaks | request blocked: surrogate present, destination not bound |
| Ask the bound service to echo the key back, then leak it | key leaks | the response is scrubbed to the surrogate |
| Use the key against the bound host for something else | works | still works (the child *is* the user's proxy to that service) |
| Steal the key from disk or the keychain | not exposed by the child | not exposed by the child; `rigd` holds it in memory for the session |
| Talk to `rigd` from another local process to obtain surrogates or the CA | n/a | needs the broker token (a private file); a browser or a DNS-rebinding page is refused before the token is checked |
| Read the broker token file (the child runs as you) and open a session that binds the key to the attacker's host | key leaks | **stopped** (§3a): a session request carries no hosts; the broker takes bindings from the policy `rigfile apply` approved. Residual: a same-user process can still borrow another *approved* server's session and spend that key at ITS bound host — narrowed by `--confine` when the borrowing process is a server Rigfile itself launched (§8) |
| Make the child trust the CA globally | n/a | the CA is only ever given to that child's environment |

**What Level 2 does not stop:** misuse of the key *against the bound host* (an agent told to place trades, delete repositories, send email through the service the key is for). The broker narrows where a secret can go, not what the secret's owner can be made to do. Permissions on the key itself (read-only tokens, paper-trading keys) remain the user's first defence.

## 2. Components

```
rigfile exec ──(1) POST /v1/sessions (bearer token)──► rigd broker API  (127.0.0.1:P, token in a private file)
    │                                                     │  reads real secrets from the secret store
    │◄──(2) {proxy_url, ca_pem, surrogates, session_id}───┘
    │
    └─ launches the MCP server with:
         env KEY=<surrogate>            HTTPS_PROXY=http://<session>:<secret>@127.0.0.1:Q
         NODE_EXTRA_CA_CERTS / SSL_CERT_FILE / REQUESTS_CA_BUNDLE / CURL_CA_BUNDLE = <ca file for this child>
                                     │
                    MCP server ──CONNECT host:443──► rigd proxy (127.0.0.1:Q)
                                     │  1. proxy auth → session
                                     │  2. host in server's network.allow? else 403 + log
                                     │  3. TLS interception with a leaf cert for host (signed by the local CA)
                                     │  4. requests: swap surrogate→real iff host matches that secret's bound hosts
                                     │  5. forward to the real host (verifies the real certificate)
                                     │  6. responses: real→surrogate; log the decision
```

`rigd` is one process (`rigfile broker run`) hosting both listeners. The CA key lives only inside it.

## 3. Surrogates

`rgs_sur_` + 32 random characters (URL-safe), minted per session per secret. The session table maps surrogate → (secret ref, bound host patterns). A surrogate carries no information and cannot be derived from the real value. Real values are read from the secret store when a session is created and kept in memory until it ends.

Surrogates have a recognisable prefix so that (a) the proxy can find them cheaply and (b) a **surrogate arriving at any host it is not bound to is detectable** and blocks the request even when the destination is otherwise allowed.

## 3a. Where bindings come from: the approved policy

A session request is `{server, secrets:[{env, ref}]}` and **nothing else**: no hosts, no allowlist (the API refuses a request that has them). The broker builds the session from the policy stored in `state.json` under `broker`, which `rigfile apply` writes from the rig itself: for each stdio MCP server that declares `network.allow`, the allowlist, the program it is launched with (its package registries are added: `npx` gets `registry.npmjs.org`), and for each secret variable the reference and the hosts from `secrets.<ref>.hosts`. A server two targets define differently, or with a secret that has no bound hosts, is left out, so the broker refuses it (fail closed). `rigfile exec` still receives `--allow`/`--bind` in the MCP config entry (they decide whether the launch must be Level 2, and document the policy), but the broker ignores whatever hosts a caller supplies: editing a config file cannot redirect a key, and neither can a compromised child holding the token. Policy lives in `state.json`, so `rigfile rollback` reverts it together with the entries it describes. Tests: `TestBrokerBuildsSessionsOnlyFromTheApprovedPolicy`, `TestApplyApprovesTheBrokerPolicyFromTheRig`, `TestExecLevel2RefusesWhatItCannotProtect`, and the four "broker's own doors" rows of the red team.

## 4. Host patterns

Bound-host and allowlist patterns are the schema's `hostPattern`: an exact host, or `*.example.com` (one or more labels, never the apex), optionally with a port (`api.example.com:8443`; default 443). Matching is on the **connect target**, lower-cased, IDNA-normalised to ASCII, without a trailing dot. IP literals match only an identical IP literal pattern; a name never matches an IP. Patterns with `*` anywhere but a whole leading label are refused at load time.

## 5. What the proxy does with a request

1. **Authentication:** the proxy requires `Proxy-Authorization: Basic` with the session's credentials; anything else is `407`. Peers must be loopback.
2. **CONNECT policy:** the target host:port must match the server's `network.allow` (an empty list means the server declared no policy: the broker then refuses to create a Level 2 session and the launch falls back, §7). Not allowed: `403`, logged `blocked: host not allowed`.
3. **Interception:** `200 Connection Established`, then TLS with a leaf for the host (SAN = the host; ECDSA P-256; 24 h validity; cached), ALPN `http/1.1` only.
4. **Request rewriting:** for each request on the connection: surrogates in header values, the request target's query, and (for `Content-Type` text/*, JSON, form-urlencoded, XML, up to 1 MiB) the body are located. If **any** surrogate found is bound to patterns that do not match the host, the request is not forwarded: `403` to the child, logged `blocked: surrogate <ref> not bound to <host>`. Otherwise each surrogate is replaced by its real value, `Content-Length` is recomputed, and the request goes upstream. Surrogates in other places (binary bodies, streams) are not touched, and are logged as `unswapped`.
5. **Upstream:** a normal TLS client with the system trust store verifying the real host's certificate (a failure is a `502` and logged). `Proxy-Authorization`, `Connection` and hop-by-hop headers are dropped. Redirects are not followed by the proxy (the child decides, and a redirect to another host is a new CONNECT, checked again).
6. **Response rewriting:** for text/JSON responses up to 1 MiB, occurrences of any real value of this session are replaced by its surrogate; the same in header values. Larger or binary responses stream through.
7. **Audit:** one JSON line per request or CONNECT decision (§6).

## 6. Audit log

`<state>/rigd/audit.jsonl`, created `0600` (user-only ACL on Windows), rotated at 10 MiB (three files kept). Fields: `time`, `session`, `server`, `method`, `host`, `path` (never the query), `status`, `decision` (`allowed` | `swapped` | `blocked`), `reason`, `secrets` (names only). No header values, bodies, surrogates or real values, ever. A test greps the log and every API response for the real test secret.

## 7. Levels and fallback

- **Level 2** requires: the broker enabled (`rigfile broker enable`, which is opt-in) and running, and the server having a `network.allow` list (so there is something to enforce).
- **Level 1** (today's `rigfile exec`) is used when the broker is off or not running, or the server is opted out (`rigfile broker exclude <server>`, for tools that ignore the CA variables). It prints a one-line notice on stderr and `rigfile doctor` shows the level per server: `L2 protected`, `L1 (real key in the process)`, `no secrets`.
- A server that **declares** `network.allow` and whose session cannot be created **fails to start** with the reason instead of silently running with the real key; the person can choose `broker exclude` explicitly.
- **As built (S7-M4).** `rigfile exec` takes `--server NAME`, `--allow host[,host]` and `--bind ref=host[,host]` (adapters write them from `mcp_servers.<name>.network.allow` and `secrets.<ref>.hosts`; a server with no `network.allow` gets none, so its entry is the Level 1 entry). The decision, in order: no secrets → nothing to protect; Level 2 not enabled → Level 1, silent (unchanged behaviour); server excluded → Level 1 with a notice; no `network.allow` → Level 1 with a notice; **broker not running, or the session refused → the server does not start**, and the message names `rigfile broker run` and `rigfile broker exclude <server>` (the person opted in, so a silent downgrade would defeat the point). The real value is read by the broker, never by `exec`.
- The child gets `HTTPS_PROXY`/`https_proxy` (with the session credentials), empty `NO_PROXY`, `NODE_USE_ENV_PROXY=1` (Node's built-in `fetch` only follows proxy variables when asked), and `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO` pointing at a per-launch CA file in a private temp directory that is removed when the child exits. The session is closed when the child exits; if `rigfile exec` is killed outright the session expires after 24 h.
- **Package launchers.** `npx`/`bunx`/`pnpm`/`yarn` and `uvx`/`pipx` must download the server before it starts, through the same proxy. Their registry hosts (`registry.npmjs.org`; `pypi.org`, `files.pythonhosted.org`) are added to the session's allowlist automatically. No secret is bound to them, so a key cannot be sent there; the cost is that a compromised server can also talk to the package registry.
- `rigfile doctor` shows each wrapped server as `no secrets`, `L1 (...)` (warning), `L2 protected` or `L2, broker not running: will refuse to start` (failure).

## 8. Limits (documented, tested where testable)

- Only HTTP/1.1 over TLS, as seen by the child; upstream may use HTTP/2.
- Programs that do not honour `HTTPS_PROXY` and the CA variables bypass the proxy; **without a proxy there is no swap, so they would send the surrogate and fail** (they cannot leak the real key, but they do not work: use `broker exclude`).
- Go programs on macOS and Windows use the OS trust store and ignore `SSL_CERT_FILE`; Java uses its own keystore; certificate-pinning clients reject the interception. These are the Level 1 cases.
- HTTP/3, WebSockets, gRPC over h2c and raw sockets are not intercepted (WebSocket upgrades are refused rather than tunnelled uninspected when a surrogate is involved).
- `rigd` must be running for Level 2; if it dies mid-session the child's requests fail (closed), they do not bypass.
- **A same-user attacker can still borrow an approved server's session — narrowed 2026-09-27 by `rigfile exec --confine` (owner decision).** The token file is readable by every process running as you, so in general a same-user process (not necessarily one Rigfile launched) can ask the broker for a session as any server you applied and spend that server's key at the host it is bound to (never elsewhere: bindings come from the approved policy, §3a). It cannot bind a key to a host of its choosing, request a server or a secret that was not approved, or make the policy larger without editing `state.json` (which `base-secure` denies the agent, and which `rigfile doctor` does not currently re-verify).

  The threat model this broker exists for (§1) is specifically "the attacker controls the MCP server process" — i.e. the borrowing attacker, in the realistic case, *is* a server Rigfile itself launched, gone bad. `--confine` closes exactly that case: it runs the launched server under a network-only sandbox (macOS `sandbox-exec`, Linux Landlock) that permits outbound TCP only to the broker's own CONNECT proxy, never to its control API, so a malicious server cannot itself call `Open` for another server's session. It does not protect against a wholly separate process on the machine — one Rigfile never launched — reading the token file directly and calling the API from outside any sandbox; that residual needs OS-level separation (the broker under another account, or a keychain access-control list for the token), and remains open. Filesystem access is never restricted by `--confine`, on purpose: a generous allow-list needed to avoid breaking arbitrary MCP servers would undercut the guarantee, so the scope is deliberately just the network path that matters. Fails closed: if the platform or kernel cannot honour it, the server does not start. Verified live on macOS (`internal/sandbox`, `TestExecConfineBlocksTheBrokerControlAPI`: a confined child really can dial the proxy and really cannot dial the control API) and, now, on real Linux Landlock ABI 4+ hardware (Oracle Cloud Ampere A1, Ubuntu 24.04, kernel 6.17, 2026-09-28: same test, same result, 5/5 clean runs). **Linux history, for whoever reads this later:** an earlier real CI failure on GitHub Actions' `ubuntu-latest` (which does answer Landlock ABI ≥ 4, a kernel this project had never accounted for) looked exactly like broken per-port enforcement — a canary port deliberately left out of the ruleset was correctly denied, but the broker's own control-API port, also never added, was reachable anyway. `createNetRuleset`/`restrictSelf`/`LandlockExecMain` (`internal/sandbox/sandbox_linux.go`) were re-checked line by line against the live kernel UAPI header at the time and found correct, which they were: the actual cause, found once real ABI 4+ hardware was available to debug on directly, was that `TestMain` (`cmd/rigfile/githooks_e2e_test.go`) wasn't dispatching the confinement mechanism's re-exec'd hidden subcommand to `LandlockExecMain` at all when running under `go test` — so the "confined" child in that failure was never actually restricted in the first place, self-check included. Fixed there (`TestMain` now recognizes `sandbox.LandlockExecSubcommand` unconditionally, matching what `main.go`'s real dispatch already did); `sandbox_linux.go`'s own logic needed no change and Linux confinement is enabled normally, same as any other platform. Opt-in, per launch (`rigfile exec --confine`); not wired into the manifest schema yet, so applying a rig does not turn it on for you. Mitigations that remain yours regardless: read-only or paper-trading keys, and the audit log names the server of every session.
- The broker holds real secrets in memory while sessions exist; a local attacker with the user's rights (debugger, memory read) can take them. It is not a defence against a compromised user account, only against a compromised *child process*.

## 9. Tests (named)

| Rule | Test |
|---|---|
| The CA and leaf certificates are correct and constrained | `TestCA*` |
| Host patterns match as specified, including edge cases | `TestHostPatterns` |
| Surrogates are unique, unguessable, session-scoped and expire | `TestSurrogates*` |
| Swapped only for the bound host; blocked elsewhere; blocked for a not-allowed host | `TestProxySwapsOnlyForTheBoundHost`, `TestProxyBlocksDisallowedHosts`, `TestProxyBlocksSurrogateToWrongHost` |
| Responses that echo the real secret are scrubbed | `TestProxyScrubsEchoedSecrets` |
| Body handling: chunked, gzip, large, binary | `TestProxyBodies` |
| Proxy authentication and loopback-only | `TestProxyAuth` |
| The audit log never contains a value | `TestAuditNeverContainsSecrets` |
| `--confine` blocks a launched server's own network access to the broker's control API while leaving its proxy access intact (macOS, live); fails closed everywhere the platform/kernel cannot honour it | `TestExecConfineBlocksTheBrokerControlAPI`, `TestConfinementActuallyRestrictsNetwork`, `TestWrapFailsClosedWhenLandlockUnavailable`, `TestWrapFailsClosedBelowRequiredABI`, `TestConfineWrapsTheCommand`, `TestConfineFailsClosed` (`internal/sandbox`, `internal/execshim`) |
| The broker API authenticates, expires and cleans up sessions, and never returns real values | `TestBroker*` |
| Frontings, upgrades, non-loopback peers, internal addresses | `TestProxyRefusesFrontingAndUpgrades`, `TestProxyRefusesNonLoopbackPeers`, `TestDefaultDialRefusesInternalAddressesForNames` |
| `exec` sets the environment, ends the session, and falls back or fails as specified | `TestExecLevel2*` |
| Service files per OS | `TestServiceFiles` |
| A malicious MCP server cannot exfiltrate the real key (32 attempts, real child process, real broker; results in `docs/red-team-broker.md`) | `TestRedTeamBroker` |
