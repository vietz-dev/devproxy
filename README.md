# devproxy

Local HTTPS names for services running in multiple Git worktrees. One Caddy
instance handles TLS and reverse proxying. `devproxy` runs a small dashboard,
registers routes with Caddy, and wraps development commands with an available
port. It does not proxy application traffic itself.

This is an early local development tool. It currently targets macOS and Linux
with services running on the host. A service inside Docker can also be
registered when its HTTP port is published to the host.

## Build

Go 1.24 or newer is required to build the binary:

```sh
go build -o bin/devproxy ./cmd/devproxy
```

`bin/devproxy` is the only program a project needs for registration. It has no
Node.js or pnpm runtime dependency.

## Start Caddy once

`devproxy serve` requires a **dedicated Caddy instance** whose admin endpoint
is available at `http://127.0.0.1:2019`. It owns that instance's configuration;
do not point it at a Caddy instance serving unrelated sites. Caddy also needs
access to ports 443 and 2019. Stop any per-project Caddy or Portless service
using 443 first.

The included Compose file runs Caddy with host networking so it can reach
development processes bound to `127.0.0.1`. On Linux, host networking works
with Docker Engine. OrbStack supports it on macOS; for Docker Desktop, enable
**Host networking** in its network settings first. Then run:

```sh
docker compose -f caddy/compose.yml up -d
bin/devproxy serve
```

Keep `devproxy serve` running in a terminal for now. Open
<https://devproxy.localhost> for the dashboard. The Compose volume preserves
Caddy's local certificate authority across restarts. To trust its root on
macOS, after Caddy has served the first HTTPS request:

```sh
docker compose -f caddy/compose.yml cp caddy:/data/caddy/pki/authorities/local/root.crt ./devproxy-root.crt
security add-trusted-cert -d -r trustRoot -k "$HOME/Library/Keychains/login.keychain-db" ./devproxy-root.crt
```

The trust command may prompt for your macOS password. Other operating systems
need their own trust-store step. The copied certificate is ignored by Git.

You can also run Caddy natively with the included `caddy/Caddyfile`; the
daemon still connects to its local admin endpoint. Caddy supports reloading a
Caddyfile through `POST /load`, which is how registrations are applied.

## Register a service

Run this from a project's Git worktree. `--project` is a stable name shared
by all of that project's worktrees; the worktree name comes from the checkout
folder. The URL is `https://<project>-<worktree>.localhost`.

```sh
devproxy run --project kasta web -- next dev -p '{port}'
```

`run` chooses a free port, sets `PORT` and `DEVPROXY_URL`, replaces `{port}` in
arguments, starts the command, waits for the port to open, and registers the
route. It renews the registration while running and removes it on exit. The
service must listen on the chosen port. Use `--url-env NEXT_PUBLIC_APP_URL`
when an app needs its public origin in that environment variable.

Several services can share one origin by registering different path prefixes:

```sh
devproxy run --project example --route /api api -- my-api --port '{port}'
devproxy run --project example --route / web -- my-web --port '{port}'
```

The API receives `/api` and `/api/...`; the web service receives everything
else. Prefixes are preserved when forwarded. Caddy handles WebSocket upgrades
for frontend hot reloads.

For a service already running on a known host port, use `register` and leave
that command running. This works for an API, frontend, nginx, or a container
whose port is published on the host:

```sh
devproxy register --project example --route /api --port 3001 api
```

`devproxy list` prints current registrations. Registrations expire after 20
seconds if their client disappears without unregistering. When the daemon
restarts, running clients register again on their next heartbeat.

## Kasta example

Build the binary in this repository, then run this from the Kasta root in
another terminal after starting Caddy and `devproxy serve`:

```sh
tools/with-env.sh ./devproxy/bin/devproxy run --project kasta --url-env NEXT_PUBLIC_APP_URL web -- pnpm --dir apps/web exec next dev -p '{port}'
```

The existing `pnpm dev` command still uses Portless. The example above lets
you try devproxy without changing Kasta's development scripts.

## Current limits

- Caddy must be running before `devproxy serve` starts. The daemon retries
  Caddy updates after a later Caddy restart.
- `run` expects the service to accept an allocated port through `PORT` or a
  `{port}` argument. It does not rewrite a service's own configuration.
- Names are single DNS labels. Two checkouts with the same project and folder
  name cannot register the same route simultaneously.
- The daemon's control endpoint listens only on `127.0.0.1:7654`. Treat this
  as a local developer tool; the Caddy admin endpoint should also stay local.

## Verify changes

```sh
go test ./...
DEVPROXY_TEST_CADDY=1 go test ./...  # also tests Caddy config and routing in Docker
```
