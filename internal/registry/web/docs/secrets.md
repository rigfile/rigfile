# Secrets

Rigs are meant to be shared, so they never contain secret values. They contain **references**, and each machine supplies its own values.

## References

In a manifest, any MCP server environment value, bearer token or header that is a secret is written as a reference:

```yaml
mcp_servers:
  alpaca:
    command: npx
    args: ["-y", "@example/alpaca-mcp-server@1.4.2"]
    env:
      ALPACA_API_KEY: secret://alpaca/api_key

secrets:
  alpaca/api_key:
    description: Alpaca API key (paper)
    obtain_url: https://app.alpaca.markets/paper/dashboard/overview
    hosts: ["*.alpaca.markets"]
```

A reference is `secret://` followed by a path of lower-case segments (`alpaca/api_key`). The `secrets:` block declares each one: what it is, where to get it, and which hosts it may be sent to.

The validator refuses a manifest that has something that looks like a real key where a reference belongs, and `publish` blocks any upload in which the secret scanner finds a value.

## Storing values

```sh
rigfile secrets set alpaca/api_key     # prompts; the value is never echoed or printed
rigfile secrets status alpaca/api_key  # whether it is set (never the value)
rigfile secrets list                   # stored references, names only
rigfile secrets rm alpaca/api_key
```

Values go into your operating system's keychain (macOS Keychain, the Windows Credential Manager, or the Secret Service on Linux). Where no keychain is available (a server, a container), Rigfile falls back to an **encrypted file** protected by a passphrase; `rigfile doctor` flags this as weaker. For unattended use, point `RIGFILE_PASSPHRASE_FILE` at a file only you can read (mode `0600`); set `RIGFILE_SECRETS_BACKEND=file` to use the file store deliberately.

## How values reach a server

When you apply a rig, Rigfile registers each stdio MCP server so that the tool starts it through `rigfile exec`:

```sh
rigfile exec --secret ALPACA_API_KEY=alpaca/api_key -- npx -y @example/alpaca-mcp-server@1.4.2
```

`exec` reads the values and puts them into **that process's environment only**. They are never written into the tool's configuration files, never passed on a command line, and never printed. You can use `exec` yourself for any command:

```sh
rigfile exec --secret GITHUB_TOKEN=github/token -- gh api user
```

## The broker (optional, stronger)

With the default (Level 1), the real key is in the MCP server's environment, so a compromised server could read it and send it anywhere. The optional **secret broker** (Level 2) closes most of that gap:

```sh
rigfile broker install     # a per-user service (launchd, systemd --user, or a scheduled task)
rigfile broker enable      # turn Level 2 on for rigfile exec
rigfile broker status
```

At Level 2 a server gets a **surrogate** value instead of the real key, and its traffic goes through a local proxy. The proxy swaps in the real key only on requests to the hosts the secret is bound to (the `hosts:` of its declaration), and only for servers you approved. A server that sends the surrogate anywhere else leaks nothing usable. Every substitution is written to a local audit log.

If a server does not work behind a proxy, run just that one at Level 1: `rigfile broker exclude <server>`.

`rigfile exec --confine` additionally sandboxes a server launched at Level 2 so it can reach the proxy but not the broker's control API (macOS and Linux; it fails closed where the sandbox is unavailable).

Known limits of the broker, stated plainly:

- Programs that do not use the system proxy or trust store for TLS (for example Go programs on macOS and Windows, Java, and clients that pin certificates) cannot be brokered; exclude them.
- HTTP/2 to the server and WebSockets are not covered.
- A separate process of yours that was never started through `rigfile exec` is outside the broker's reach.

## Secrets in git

base-secure installs pre-commit and pre-push secret scanning for your repositories, and `rigfile doctor --git [<repo>]` scans a repository's whole history for committed secrets and walks you through rotating them (read-only).
