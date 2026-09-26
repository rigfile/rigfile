# Stage 7 plan of record: `rigd`, the secret broker

**Goal (RIGFILE_PLAN.md §12):** the agent never holds real secrets. A local daemon hands MCP servers *surrogate* values, routes their outbound HTTPS through an egress proxy, and swaps a surrogate for the real secret only on a request to the host that secret is bound to.
**Exit:** a red team on macOS, Linux and Windows: a deliberately malicious MCP server tries to exfiltrate its key to an attacker host, and only a surrogate leaks, the request is blocked, and it is logged.

Started 2026-09-26 on branch `stage-7`, created from `stage-6` (which is pushed and awaiting merge). Spec: `docs/rigd.md`.

**What I can and cannot do.** I can build and test the broker end to end on this Mac and, through CI, on Linux and Windows: the CA, surrogates, the intercepting proxy, the session API, `rigfile exec` integration, service file generation, and the malicious-server red team. I cannot: install a LaunchAgent, a systemd unit or a scheduled task on your machine as part of a test (that is your real machine), test against real vendor APIs, or judge which real MCP servers tolerate an intercepting proxy. Those are S7-M7 (owner).

## Design calls (decided by my recommendation; each deviates from or sharpens the plan where noted)

1. **The CA is trusted per child process, never by the operating system.** The plan proposed macOS Keychain, Windows store and Linux bundle trust. That changes what *every* program on the machine trusts, which is exactly the wrong blast radius for a secret broker. Instead `rigfile exec` sets `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO` (and friends) **for the one child it launches**. The CA private key never leaves `rigd`. Cost: programs that ignore those variables (Go programs on macOS and Windows, which use the OS verifier; Java; anything that pins certificates) cannot use Level 2 and fall back to Level 1.
2. **One session per launch.** `rigfile exec` asks `rigd` for a session (the server's name, its secrets with their host bindings, its allowed hosts) and receives surrogate values, a proxy URL with per-session credentials, and the CA certificate. Surrogates are random, unique per session and worthless outside it; the session ends when the child exits (or after 24 h).
3. **Secret ↔ host binding is enforced by the proxy, not by the child.** A surrogate is replaced by the real value only in a request whose destination host matches the secret's bound host patterns (`secrets.<ref>.hosts`) **and** the server's `network.allow`. A surrogate sent anywhere else blocks the whole request and is logged. A destination outside `network.allow` is refused at CONNECT.
4. **Substitution scope:** request headers, the URL query, and text or JSON request bodies up to 1 MiB (content length recomputed). Binary bodies, streaming uploads, WebSockets and HTTP/3 are passed through untouched, which means a surrogate inside one is *never* swapped (it fails closed: the API rejects a surrogate key).
5. **Responses are scrubbed.** If an API echoes the real secret back (an "echo" endpoint, an error message), `rigd` replaces it with the surrogate before the child sees it, so the child cannot learn the real value by asking the server.
6. **Fail closed, and say so.** No session and no proxy means the server runs at Level 1 with a visible notice and a `doctor` line. A session that cannot be created is not silently downgraded when the rig declared `network.allow` for that server: the launch fails with the reason (the person can opt the server out explicitly).
7. **The broker API is loopback-only with a bearer token in a private file** (`0600`; user-only ACL on Windows), so no other local user and no browser page can reach it. It is the same on every OS; no sockets that differ per platform.
8. **Service install is generation plus an injectable activator.** launchd (macOS), `systemd --user` (Linux), a per-user scheduled task (Windows) are generated as files and written through the journaled writer (backups, rollback); the activation commands run through a seam that tests replace. Activating a service on a real machine is an owner-run check.
9. **The audit log records decisions, never values**: time, server, method, host, path without the query, status, decision (`allowed`, `swapped`, `blocked`), the *names* of the secrets swapped or blocked.

## Milestones

| # | Milestone | Delivers | Acceptance | Status |
|---|---|---|---|---|
| S7-M0 | **Spec** | `docs/rigd.md`: protocol, session model, substitution rules, threat model, limits | Every rule has a named test | todo |
| S7-M1 | **CA, surrogates, policy** | `internal/rigd`: local CA (ECDSA), per-host leaf certificates, surrogate minting and lookup, host-pattern matching, session store | Unit tests: certificate validity and constraints, pattern edge cases (wildcards, ports, IP literals, punycode), surrogate uniqueness and expiry | todo |
| S7-M2 | **Intercepting proxy** | HTTP proxy with CONNECT, TLS interception for allowed hosts, swap in headers, query and bodies, response scrubbing, blocks, audit log | Tests against a local TLS upstream: swapped only for the bound host; blocked elsewhere; echoed secrets scrubbed; chunked, gzip, large and binary bodies; keep-alive; slow clients | todo |
| S7-M3 | **Broker service** | `rigfile broker run`: session API on loopback with token file, secrets read from the secret store, audit log, status | Auth, expiry and cleanup tests; no secret in any API response | todo |
| S7-M4 | **`rigfile exec` integration** | `exec` opens a session, sets the child's environment (proxy, CA variables, surrogates), closes it on exit; adapters emit hosts and allowlists; per-server fallback; `doctor` shows the level | Real child processes in tests; fallback and failure paths; goldens per target | todo |
| S7-M5 | **Service install** | `rigfile broker install|uninstall|start|stop|status` with launchd, systemd --user and Windows task files; plain background mode where there is no service manager | Golden files per OS; fake activator; journaled writes with rollback | todo |
| S7-M6 | **Red team** | a deliberately malicious MCP server (attacker host, echo tricks, surrogate misuse, DNS and IP-literal tricks, redirects) run against the broker; results in `docs/red-team.md` | Passes on macOS and Linux locally and on all three OSes in CI | todo |
| S7-M7 | **Owner gate** | `docs/stage-7-owner-checks.md`: real service install per OS, real MCP servers and vendor APIs, decisions on opt-outs | Exit criteria above | todo (owner) |
