# Working on devproxy

devproxy is a local HTTPS router and process supervisor for services in Git
worktrees. Kata is the task ledger for agent work on this repository. Keep these
responsibilities separate: do not add issue tracking or agent messaging to the
devproxy daemon to mirror Kata.

## Start work

1. Run `kata quickstart --format agent` and `kata ready --agent`.
2. Search Kata before creating an issue. Claim the issue you work on. Record
   decisions or handoffs as comments; close only after verification, with
   specific evidence. Use `KATA_TEAMMATE` for delegated agents.
3. Read `README.md` for the runtime model. Use `devproxy --help` as the CLI
   contract, `just build` to build the dashboard and binary, and `just test`
   to verify Go changes.

## Design rules

- CLI commands own workflows. The browser reads the same daemon API and helps
  people inspect local services; it does not maintain a separate state model.
- Keep the Go binary self-contained. Frontend dependencies are build-time only.
- Preserve local-only control and Caddy admin endpoints. Do not expose register,
  heartbeat, or unregister through the public `devproxy.localhost` site.
- A live registration means its client renewed a lease. It does not prove the
  upstream is healthy. Label these states accurately in CLI and UI.
- Keep Caddy responsible for TLS and reverse proxying. devproxy controls its
  dedicated Caddy instance and does not proxy application traffic itself.

## Code discovery

Use `mcp__semble__search` to locate code by behavior. For documentation or
configuration searches, set `content` to `docs` or `config`. Read the returned
file at its reported line before editing. Use `mcp__semble__find_related` for
similar code. Use `rg` only when every occurrence of a literal string is needed.
