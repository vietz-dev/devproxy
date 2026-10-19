---
name: devproxy
description: Use when starting or opening this project's local development server, finding its URL, listing active instances, or diagnosing local Caddy routing. This project uses devproxy to register development services.
---

# devproxy in this project

This project uses `devproxy` for local HTTPS URLs. One Caddy instance handles HTTPS and forwards requests. The `devproxy` CLI starts or registers services and maintains Caddy routes. A development command can already contain the wrapper, so inspect the project's scripts and README before starting a service.

## Start or find the project

1. Find the project's documented development command. Run that command so its `devproxy` wrapper chooses a free port and registers the service. Keep the command running while the user needs the project.
2. Run `devproxy list --json` to get active URLs, routes, service names, local ports, registration IDs, and lease expiry as structured data. Match the project name from its script to the relevant entry. Use `devproxy list` when a human-readable table is enough. The dashboard is at `https://devproxy.localhost`.
3. Give the user the registered HTTPS URL when you report that the project is ready. Use the URL from `devproxy list` or the wrapper's output.

If `devproxy` is missing, use the project's setup instructions to install the binary. If `devproxy list` cannot reach the daemon, start `devproxy serve`; it starts embedded Caddy by default. If HTTPS shows a certificate warning, follow the setup instructions to trust Caddy's local root certificate. An existing external Caddy setup can still be used with `devproxy serve --caddy-admin http://127.0.0.1:2019`; Docker Desktop also needs `--docker-desktop`.

## CLI reference

- `devproxy list` shows current registrations. A stopped service disappears when it unregisters or its registration expires.
- `devproxy list --json` emits the same registrations as JSON for scripts and agents. Use `devproxy inspect --json ID` when you need one registration's details.
- `devproxy serve` runs embedded Caddy, the local control server, and dashboard. Pass `--caddy-admin` only for a dedicated external Caddy instance.
- `devproxy run --project NAME SERVICE -- COMMAND [ARGS...]` starts a command on a free port and registers it. It sets `PORT` and `DEVPROXY_URL` for the child. Use `{port}` in an argument when the command needs an explicit port. Use `--url-env VARIABLE` to set another origin variable for the child.
- `devproxy register --project NAME --port PORT SERVICE` registers an existing service whose port is available on the host. Keep this command running while the service needs its URL.

`run` and `register` use the Git checkout folder as the default instance name. The resulting URL is `https://<project>-<folder>.localhost`. Use `--worktree NAME` before `SERVICE` when the folder name is ambiguous. Use `--route /path` before `SERVICE` to register one path prefix on a shared project URL. Read `devproxy --help` for the current command syntax.

The registration's lease expiry shows that the wrapper is still renewing it. It
does not prove that the application returns successful HTTP responses. If the
URL fails, inspect the wrapped process and Caddy as the project's setup guide
describes.
