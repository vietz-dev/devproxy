# devproxy

Local HTTPS names for services running in multiple Git worktrees. Caddy runs
inside the devproxy daemon by default and handles TLS and reverse proxying.
`devproxy` runs a small dashboard, registers routes with Caddy, and wraps
development commands with an available port. It does not proxy application
traffic itself.

This is an early local development tool. It currently targets macOS and Linux
with services running on the host. A service inside Docker can also be
registered when its HTTP port is published to the host.

The [developer documentation](https://devproxy.vietz.dev/docs/) covers setup,
CLI commands, routing, troubleshooting, and contributing. Run it locally from
`docs/` with `npm install` and `npm run dev`, then open
<http://localhost:3000/docs>.

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
`DEVPROXY_INSTALL_DIR="$HOME/bin" just install`. A running daemon keeps using
its old executable until you restart it with `devproxy stop` and `devproxy start`.

## Install the agent skill in a project

The skill in `skills/devproxy/SKILL.md` tells an agent how to start a project
that uses devproxy, find its URL, and list active services. From the project
that will use devproxy, install the skill with the
[`skills` CLI](https://skills.sh/docs):

```sh
npx skills add /absolute/path/to/devproxy --skill devproxy
```

Select the agent you use when the CLI asks. The skill is installed in that
project, so new agent sessions can use it. To install from GitHub, run
`npx skills add vietz-dev/devproxy --skill devproxy` instead.

## Start the proxy

After installing, start the shared background daemon once:

```sh
devproxy start
devproxy status
```

`start` waits for Caddy and the control API to become ready, then returns to
your shell. It is safe to run again from another project; an existing daemon
is reported without starting a second one. The daemon remains active after
the terminal closes. Use `devproxy stop` to shut it down. Logs are written to
the user cache directory at `devproxy/daemon.log`. To keep it in the foreground
for development, use `devproxy serve`.

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

This embedded mode is an evolutionary prototype. It has no automatic startup
at login, trust-repair command, or migration of the old Docker volume yet.
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
running in the terminal. For a detached devproxy with external Caddy, run
`devproxy start --caddy-admin http://127.0.0.1:2019 --docker-desktop` after
starting the container. If an older host-network Caddy container is already
running, Compose replaces it while preserving its certificate volume.

On Linux Docker Engine or macOS OrbStack, use the host-network Compose setup:

```sh
docker compose -f caddy/compose.yml up -d
bin/devproxy serve --caddy-admin http://127.0.0.1:2019
```

Use `devproxy start --caddy-admin http://127.0.0.1:2019` to run devproxy in the
background with this Caddy setup. Open
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
`devproxy/config.yaml` and from `.devproxy.yaml` at the current Git worktree's
root, including when invoked from a nested directory. Nested project files are
not merged. Outside Git, or if Git is unavailable, only `.devproxy.yaml` in the
current directory is used; parent directories are not searched.
`XDG_CONFIG_HOME` overrides the platform user config directory. Use
`--config PATH` to read one additional file (relative paths stay relative to the
current directory, and a missing explicit file is an error). For a setting such as `daemon`,
precedence is `--daemon`, `DEVPROXY_DAEMON`, the explicit config file, the
project file, the user file, then the built-in default. Hyphenated settings
use underscores in environment variables, such as `DEVPROXY_CADDY_ADMIN`.
The CLI only reads these files; it does not create them.

```yaml
# .devproxy.yaml
version: 1
project: example
route: /
daemon: http://127.0.0.1:7654
```

### Project configuration schema (version 1)

Every `.devproxy.yaml`, including one supplied with `--config`, must declare
the integer `version: 1`. To migrate an existing project file, add that line,
remove unknown settings, and use the types below. No automatic rewrite occurs.
The file is a single, flat YAML mapping; keys are case-sensitive. Nested
settings, aliases, merge keys, duplicate keys, and additional YAML documents
are not supported.

| Type | Optional settings |
| --- | --- |
| String | `project`, `worktree`, `route`, `daemon`, `listen`, `caddy-admin`, `url-env` |
| Duration string (for example `30s` or `1m`) | `ready-timeout` |
| Integer | `port` |
| Boolean (`true` or `false`) | `docker-desktop`, `json`, `plain`, `quiet`, `no-color`, `no-input` |

Omitted settings retain the precedence above; `version` is file metadata, not
an overridable CLI setting. Unknown keys, wrong types (including null), and
unsupported versions fail before service operations, even when a higher-priority
source overrides the invalid setting. Errors name the file and correction.
Quote numeric-looking string values; write booleans and integers without quotes.
Command-specific value checks, such as valid ports and routes, still apply.

For compatibility, the user `config.yaml` and explicit `--config` files with
other names may remain unversioned and retain their permissive legacy loading.
Adding `version: 1` opts those files into the same strict schema. A version in
one file does not supply the required version for another file. Configuration
lookup uses the current Git worktree root and does not search parent directories.

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

`inspect` and `services describe` match an **exact** registration ID, hostname,
or origin URL, such as `https://example-feature.localhost` (no path or trailing
slash). Use `--route /api` to select a path registration. If a host or URL is
ambiguous, the error lists the competing IDs, hosts, and routes, sorted by ID,
and suggests an `inspect` command using one actual ID. A missing route shows
the registrations available for the same lookup; other missing lookups explain
the accepted forms. `devproxy list --json` provides all current IDs and routes.

Selection errors preserve the existing JSON schema on stderr, with empty stdout:
`{"error":{"code":"CONFLICT","exit_code":5,"message":"...","run":"...","retryable":false}}`.
Missing selections use `NOT_FOUND` and exit code 4. The keys and types are
unchanged; `message` now includes corrective context and is human-readable text,
not a parsing contract. `run` is a shell command using an actual matching ID for
ambiguity, or `list --json` for missing selections. Corrections explicitly retain
the selected daemon and, for ambiguity, the chosen route, overriding defaults
from configuration or environment variables.
Scripts should branch on `code`/`exit_code`, not parse `message`.

### JSON service readiness (schema version 1)

`run --json` and `register --json` (also under `services`) write exactly one
compact JSON object followed by a newline to stdout after the local TCP port
accepts a connection and the daemon accepts the registration. For example:

```json
{"schema_version":1,"event":"service_ready","id":"7fe98c1fc7974d55b7ebc91d8cd9a430","project":"example","worktree":"feature","service":"api","url":"https://example-feature.localhost","route":"/api","port":3001,"readiness":{"registration":"accepted","tcp":"listening","http_health":"not_checked"}}
```

- `schema_version` is an integer. Version 1 may gain fields, but existing keys,
  types, and meanings will not change. Consumers should ignore unknown fields
  and reject unsupported versions.
- `event` is `service_ready`. `id` identifies this registration, not a permanent
  service. `project`, `worktree`, and `service` give its identity. `url` is the
  public origin (without the route); `route` is the forwarded path prefix and
  `port` is the local upstream TCP port.
- `readiness.registration` is `accepted`: the daemon accepted the registration.
  `readiness.tcp` is `listening`: a local connection succeeded before registration.
  `readiness.http_health` is `not_checked`: this is **not an HTTP health check**,
  TLS verification, or a guarantee that the public URL can serve requests.
  Readiness is a point-in-time observation, not continuous health monitoring.
- Both commands **remain running** to renew the lease. Read the record as soon
  as its newline arrives; do not wait for EOF or command completion. Keep the
  process and its output pipe open. Shutdown removes the registration. Heartbeat
  recovery does not emit another readiness event.
- `--quiet` does not suppress requested JSON. Startup failures emit no readiness
  event; errors use the existing JSON error object on stderr. A failure after
  readiness does not retract the earlier record; monitor process exit too.

Devproxy diagnostics remain on stderr. `run` also preserves the child's stdout
and stderr unchanged. Its stdout therefore contains both child output and the
readiness record, and child writes (including partial lines) can interleave.
Do not treat that mixed stream as JSON or assume its first line is readiness.
For a clean JSON stream, redirect the child's stdout inside the child command,
not devproxy's stdout:

```sh
devproxy run --json --project example web -- sh -c 'exec npm run dev >&2'
devproxy register --json --project example --port 3001 api
```

Read these commands incrementally from a supervised process. Avoid command
substitution (`result=$(devproxy register ...)`) or parsers that wait for EOF;
they will wait until the service is stopped. Without `--json`, the existing
human readiness notice still goes to stderr and stdout remains child output.

## Kasta example

Install the binary on your `PATH`. After running `devproxy start` once,
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
