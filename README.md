# devproxy

Local HTTPS names for services running in multiple Git worktrees. Caddy runs
inside `devproxy serve` by default and handles TLS and reverse proxying.
`devproxy` runs a small dashboard, registers routes with Caddy, and wraps
development commands with an available port. It does not proxy application
traffic itself.

This is an early local development tool. It currently targets macOS and Linux
with services running on the host. A service inside Docker can also be
registered when its HTTP port is published to the host.

The [developer documentation](docs/README.md) covers setup, CLI commands,
routing, troubleshooting, and contributing. Run it locally from `docs/` with
`npm install` and `npm run dev`, then open <http://localhost:3000/docs>.

The [Kata-informed architecture and roadmap](docs/KATA_ROADMAP.md) describes
the planned CLI-first, agent-friendly control model and React operator UI.

## Build

Go 1.26 or newer, Node.js 22 or newer, npm, and Just are required to rebuild
the dashboard and binary together:

```sh
just build
```

`just build` installs the locked frontend dependencies, rebuilds the embedded
dashboard, and writes `bin/devproxy`. For a Go-only build from the checked-in
dashboard assets, run `just go`. Run `just test` for the Go test suite.

`bin/devproxy` is the only program a project needs for registration. It has no
Node.js or npm runtime dependency.

## Install or update from source

From this checkout, run:

```sh
just install
```

This rebuilds the dashboard and Go binary, then installs `devproxy` to
`~/.local/bin/devproxy`. Running it again updates that installation. Make sure
`~/.local/bin` is on your `PATH`. To choose another writable directory, run
`DEVPROXY_INSTALL_DIR="$HOME/bin" just install`. A running `devproxy serve`
process keeps using its old executable until you restart it.

## Install the agent skill in a project

The skill in `skills/devproxy/SKILL.md` tells an agent how to start a project
that uses devproxy, find its URL, and list active services. From the project
that will use devproxy, install the skill with the
[`skills` CLI](https://skills.sh/docs):

```sh
npx skills add /absolute/path/to/devproxy --skill devproxy
```

Select the agent you use when the CLI asks. The skill is installed in that
project, so new agent sessions can use it. Once this repository is published
at `github.com/vietz-dev/devproxy`, use
`npx skills add vietz-dev/devproxy --skill devproxy` instead.

## Start the proxy

After installing, run the single binary:

```sh
devproxy serve
```

On Linux systems that restrict ports below 1024, grant the built binary
permission to bind port 443 before running it:

```sh
sudo setcap cap_net_bind_service=+ep "$(command -v devproxy)"
```

Reapply this after updating the installed binary. A future service installer should
manage this privilege without changing the binary. On macOS, follow the OS
prompt if permission is needed.

This starts Caddy in the same process, with its admin API disabled. It listens
for HTTPS on host loopback port 443 and for local control on 127.0.0.1:7654.
Open <https://devproxy.localhost> for the dashboard. Caddy keeps its local CA
in the user config directory under `devproxy/caddy`; it may need permission to
add that CA to the system trust store. If port 443 is already in use, stop the
other proxy first. The first certificate may take a moment to become ready.

If macOS still reports an untrusted certificate after the first request, trust
the generated root certificate:

```sh
security add-trusted-cert -d -r trustRoot -k "$HOME/Library/Keychains/login.keychain-db" "$HOME/Library/Application Support/devproxy/caddy/pki/authorities/local/root.crt"
```

This embedded mode is an evolutionary prototype. It has no automatic daemon
startup, trust-repair command, or migration of the old Docker volume yet.
Existing Docker users should stop their Caddy container before trying it;
the embedded CA is new and may need to be trusted separately.

### Use an external Caddy instead

The previous dedicated-Caddy setup remains available with `--caddy-admin`.
Do not point devproxy at a Caddy instance serving unrelated sites because
devproxy replaces its configuration. On Docker Desktop for macOS, the bridge
Compose setup publishes ports 443 and 2019 on host loopback. Caddy reaches
host services through `host.docker.internal`:

```sh
just serve-desktop
```

This recipe builds the Go binary, starts Caddy, and keeps `devproxy serve`
running in the terminal. If an older host-network Caddy container is already
running, Compose replaces it while preserving its certificate volume.

On Linux Docker Engine or macOS OrbStack, use the host-network Compose setup:

```sh
docker compose -f caddy/compose.yml up -d
bin/devproxy serve --caddy-admin http://127.0.0.1:2019
```

Keep `devproxy serve` running in a terminal. Open
<https://devproxy.localhost> for the dashboard. The Compose volume preserves
Caddy's local certificate authority across restarts. To trust its root on
macOS with Docker Desktop, after Caddy has served the first HTTPS request:

```sh
docker compose -f caddy/compose.desktop.yml cp caddy:/data/caddy/pki/authorities/local/root.crt ./devproxy-root.crt
security add-trusted-cert -d -r trustRoot -k "$HOME/Library/Keychains/login.keychain-db" ./devproxy-root.crt
```

The trust command may prompt for your macOS password. Other operating systems
need their own trust-store step. The copied certificate is ignored by Git.

You can also run Caddy natively with the included `caddy/Caddyfile` and use
`--caddy-admin http://127.0.0.1:2019`. External registrations are applied via
the Caddy admin API.

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

## CLI configuration and output

The CLI reads optional YAML settings from the user config directory at
`devproxy/config.yaml` and from `.devproxy.yaml` in the current directory.
`XDG_CONFIG_HOME` overrides the platform user config directory. Use
`--config PATH` to read one additional file. For a setting such as `daemon`,
precedence is `--daemon`, `DEVPROXY_DAEMON`, the explicit config file, the
project file, the user file, then the built-in default. Hyphenated settings
use underscores in environment variables, such as `DEVPROXY_CADDY_ADMIN`.
The CLI only reads these files; it does not create them.

```yaml
# .devproxy.yaml
project: example
route: /
daemon: http://127.0.0.1:7654
```

Run `devproxy config get daemon --origin` to see the effective value and its
source. Service commands also live under `devproxy services` (`run`, `register`,
`list`, and `describe`); the original top-level commands remain available.
`list` and `inspect`/`describe` accept `--json`; `list --plain` emits tab-separated
records. A piped `list` also uses tab-separated records. Status notices and
errors go to stderr, leaving stdout for requested data and child process
output. `--json` errors use a JSON object on stderr. Invalid command usage
exits with code 2, missing registrations with 4, and ambiguous registrations
with 5. An active registration indicates a renewed lease, not the health of
its upstream service.

## Kasta example

Install the binary on your `PATH`. After starting `devproxy serve`,
run `pnpm dev` from the Kasta root. Its web script registers the worktree and
sets `NEXT_PUBLIC_APP_URL` automatically. Open
`https://kasta-<worktree-folder>.localhost`.

## Current limits

- External Caddy must be running before `devproxy serve --caddy-admin` starts.
  The daemon retries updates after a later Caddy restart.
- `run` expects the service to accept an allocated port through `PORT` or a
  `{port}` argument. It does not rewrite a service's own configuration.
- Names are single DNS labels. Two checkouts with the same project and folder
  name cannot register the same route simultaneously.
- The daemon's control endpoint listens only on `127.0.0.1:7654`. Treat this
  as a local developer tool; an external Caddy admin endpoint must stay local.

## Verify changes

```sh
go test ./...
DEVPROXY_TEST_CADDY=1 go test ./...  # also tests Caddy config and routing in Docker
```
