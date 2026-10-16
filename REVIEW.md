# devproxy review guidance

Review changes for concrete defects in the CLI, daemon, Caddy integration, and
dashboard. Report findings with a specific failure path and file location.
Focus on behavior that a change can actually affect.

- Check that register, heartbeat, and unregister remain on local control
  endpoints and cannot be reached through the public `devproxy.localhost` site.
- Check that Caddy remains responsible for TLS and application proxying, while
  devproxy manages its dedicated Caddy instance and its local control API.
- Check process startup, signal handling, shutdown, and lease renewal for
  failures that leave a service or route behind or interrupt interactive use.
- Distinguish an active registration from a healthy upstream. A renewed lease
  alone does not establish upstream health.
- Check that CLI and dashboard behavior agree on the same daemon API and that
  the Go binary does not gain a frontend runtime dependency.
- Ask for tests when they exercise changed behavior, failure modes, or an
  integration boundary. Do not request tests that only repeat source text or
  assert a constant's value.

Avoid speculative findings without a reachable failure path. Follow the
project's `AGENTS.md` and `README.md` for the runtime model and build commands.
