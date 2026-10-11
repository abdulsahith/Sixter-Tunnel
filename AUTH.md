# Sixter Auth System

## What was added

- **Accounts**: signup/login at the apex domain (sixter.in). Each user picks one
  subdomain name and gets two: `<name>.sixter.in` (tunnel) and
  `<name>-repo.sixter.in` (repo dashboard).
- **Device-flow login** (`sixter login`): works on SSH-only servers — the CLI
  prints a URL + code, you approve it from ANY device (phone/laptop), the CLI
  polls and writes `~/.sixter/config.yaml`.
- **Token security**: API tokens are random 32-byte values shown once;
  the server stores only the SHA-256 hash. Passwords use PBKDF2-SHA256 (210k iters).
- **Tunnel auth**: `/connect` now rejects tunnels without a valid token for
  that subdomain (unless the server runs with `-open` for local dev).

## Layout

```
cmd/sixter            CLI: login / whoami / logout / dispatch
cmd/tunnel-client     sixter-client (reads ~/.sixter/config.yaml)
cmd/tunnel-server     sixter-server (auth + proxy + rate limit + custom domains)
cmd/repo-client       sixter-repo   (unchanged)
cmd/repo-server       sixter-reposerver (unchanged — rewrite is next phase)
internal/auth         store.go (JSON db) / handlers.go (routes) / pages.go (HTML)
internal/config       ~/.sixter/config.yaml reader/writer
internal/tunnel       shared message types
```

## Build

```
go mod tidy
go vet ./...
make windows   # or: make linux / make all
```

## Test locally (one machine)

```
# 1. start the server in one terminal
bin/windows/sixter-server.exe -addr :8008

# 2. sign up: open http://localhost:8008/signup  (pick subdomain e.g. "sahith")

# 3. login from the CLI in a second terminal
bin/windows/sixter.exe login -api http://localhost:8008
#    → browser opens /activate, approve, CLI saves ~/.sixter/config.yaml

# 4. run a tunnel — no flags needed anymore
bin/windows/sixter.exe tunnel -port 3000
#    → connects as sahith.sixter.in with your token automatically
```

To test the proxy path locally, add a hosts-file entry
`127.0.0.1 sahith.sixter.in` and browse `http://sahith.sixter.in:8008`.

## Server flags (production)

```
sixter-server -addr :8008 -apex sixter.in -rate-limit 100 \
              -data /var/lib/sixter/auth.json -static /opt/sixter/static
```

- `-open` disables auth (dev only — never in production)
- `-domains custom.com=sub1` maps customer domains to subdomains
- Behind nginx/TLS, forward `X-Forwarded-Proto: https` so cookies are Secure
  and the CLI receives `wss://` URLs.

## Notes / next phase

- repo-server still spawns sixter-client for tunnels; since the client now
  auto-reads the config, this keeps working — but tunnels are limited to the
  user's two subdomains (by design).
- Auth store is one JSON file. Move to SQLite when user count grows.
- CSRF protection on the web forms is minimal (SameSite=Lax cookie) — add
  CSRF tokens before public launch.
