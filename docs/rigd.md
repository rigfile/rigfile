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
| Talk to `rigd` from another local process to obtain surrogates or the CA | n/a | needs the broker token (private file) and session credentials |
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

## 8. Limits (documented, tested where testable)

- Only HTTP/1.1 over TLS, as seen by the child; upstream may use HTTP/2.
- Programs that do not honour `HTTPS_PROXY` and the CA variables bypass the proxy; **without a proxy there is no swap, so they would send the surrogate and fail** (they cannot leak the real key, but they do not work: use `broker exclude`).
- Go programs on macOS and Windows use the OS trust store and ignore `SSL_CERT_FILE`; Java uses its own keystore; certificate-pinning clients reject the interception. These are the Level 1 cases.
- HTTP/3, WebSockets, gRPC over h2c and raw sockets are not intercepted (WebSocket upgrades are refused rather than tunnelled uninspected when a surrogate is involved).
- `rigd` must be running for Level 2; if it dies mid-session the child's requests fail (closed), they do not bypass.
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
| The broker API authenticates, expires and cleans up sessions, and never returns real values | `TestBroker*` |
| `exec` sets the environment, ends the session, and falls back or fails as specified | `TestExecLevel2*` |
| Service files per OS | `TestServiceFiles` |
| A malicious MCP server cannot exfiltrate the real key | `TestRedTeam*` |
