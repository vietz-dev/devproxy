# Kata-informed direction for devproxy

Devproxy gives each development service in a Git worktree a stable local HTTPS URL. It starts or registers the service, keeps its route in Caddy current, and reports the result. Kata is a useful reference for making this workflow dependable for coding agents, but its domain is different: Kata tracks work; devproxy owns local service and route lifecycles.

## Product boundary

| Concern | Owner |
| --- | --- |
| Issues, dependencies, comments, actor and teammate attribution, attention requests, and evidence-backed completion | Kata, when a team uses it |
| Worktree identity, service processes, allocated ports, health, Caddy routes, URLs, and local runtime events | Devproxy |
| Launching and supervising coding-agent processes, mapping agents to worktrees and Kata issues | An external agent harness or supervisor that calls both CLIs |

Kata deliberately leaves worktree creation and process supervision to an external launcher ([orchestration boundary](https://github.com/kenn-io/kata/blob/0e8c930/docs/operations/agent-orchestration.md#L8-L31)). Devproxy should expose reliable controls and observations to that launcher. A Kata issue reference may be attached to a devproxy worktree or service as an optional link, but must not become the service's identity or a prerequisite for starting it. Devproxy's route registry must remain useful without Kata.

## Design rules

1. **CLI first.** Every operational action available in the browser must have a CLI command with the same result and validation. Keep human output readable, add complete versioned `--json` output, and use stable error kinds and exit codes. A compact `--agent` mode can reduce transcript noise once its format has a documented compatibility contract. Kata's [output contract](https://github.com/kenn-io/kata/blob/0e8c930/docs/reference/agent-output.md#L7-L88) is the reference, not a format to copy verbatim.
2. **One application API.** The CLI and browser should use the same typed local service API for state changes and reads. The API owns route validation, ownership checks, idempotent retries, and event order; UI code does not become a second control plane. Keep registration and heartbeat controls off the public `devproxy.localhost` site and preserve local-only access to Caddy administration. Kata uses this [single-access-path design](https://github.com/kenn-io/kata/blob/0e8c930/docs/design/architecture.md#L45-L53).
3. **Explicit identities and safe retries.** Identify a service instance by a stable ID distinct from its display name and URL. Require an explicit project/worktree context for mutations when inference is ambiguous. Give retryable start, register, and stop requests idempotency keys with request fingerprints so a repeated request returns the existing result and conflicting reuse fails. Kata's [idempotency rules](https://github.com/kenn-io/kata/blob/0e8c930/docs/design/data-model.md#L97-L113) show the failure behavior.
4. **Observable state.** Record service and route lifecycle events with actor, time, instance ID, and before/after state. Provide a cursor-based event read and a live stream so CLI, browser, and agent supervisors can recover after disconnects. Keep desired route state separate from observed process, health, and Caddy state; a failed route apply must be visible. Kata's [event model](https://github.com/kenn-io/kata/blob/0e8c930/docs/design/data-model.md#L26-L43) and [polling/stream reset rule](https://github.com/kenn-io/kata/blob/0e8c930/docs/design/data-model.md#L70-L94) are useful references.
5. **Evidence before reporting success.** A renewed registration lease shows that its client is live, not that the upstream is healthy. `run` or `register` should report ready only after the service accepts connections and the intended route is applied. Status should distinguish starting, healthy, degraded, and stopped with a reason and last observation. Agent handoffs should include the URL and the checks performed. Kata's [close discipline](https://github.com/kenn-io/kata/blob/0e8c930/docs/guide/concepts.md#L106-L121) informs this rule; devproxy does not close Kata issues on its own.

## Agent workflow

The supervisor creates or selects a Kata issue, starts an agent in a worktree, and records the issue ref and branch in its own session record. The agent starts services with devproxy CLI commands, reads `status` and `events` for readiness, and shares the resulting URL. The supervisor follows Kata for task progress and attention, and devproxy for process and route health. On completion it verifies the work, closes the Kata issue with appropriate evidence, and stops only the service instances it owns.

Kata's `work.attention` convention and `wait` support a fan-out/join workflow ([recipe](https://github.com/kenn-io/kata/blob/0e8c930/docs/operations/agent-orchestration.md#L45-L74), [join](https://github.com/kenn-io/kata/blob/0e8c930/docs/operations/agent-orchestration.md#L187-L236)). Its exact-recipient inbox requests are attention signals, not a reliable command queue ([teammate workflow](https://github.com/kenn-io/kata/blob/0e8c930/docs/workflows/agents.md#L432-L507)). The supervisor must retain its own runtime mapping and process lifecycle state. An optional integration may show linked Kata issue and attention state in devproxy, but should read it through Kata's CLI or API.

## Delivery phases

| Phase | Work | Acceptance criteria |
| --- | --- | --- |
| 1. CLI contract | Define resource names, stable IDs, command verbs, JSON schemas, error kinds, and exit codes for `run`, `register`, `list`, `status`, `stop`, and diagnostics. | A script can start or register a service, obtain its ID and URL from JSON, identify a failed command by error kind, and retry without duplicate registrations. Existing human commands remain usable. |
| 2. Shared control API | Put CLI and browser reads and mutations behind one typed local API; add explicit instance state, revision checks, and a durable or resumable event cursor. | CLI and API produce the same validation result for the same request; a reconnecting client can reconstruct current state after missed events; concurrent updates cannot silently overwrite each other. |
| 3. Agent supervision contract | Publish a small command recipe and machine-readable status/events for launchers; support an optional external issue ref on sessions or instances. | Two agents in separate worktrees can run services with distinct IDs and URLs; a supervisor can tell which service is ready or failed, find its owning worktree, and stop one without affecting the other. The recipe works without Kata and documents how to link it when present. |
| 4. React operator UI | Build a browser client with React, Chakra UI, TanStack Query, TanStack Form, and TanStack Table. Use service/worktree collections, detail and event views, route and health controls, and URL-backed filters. | Every UI mutation calls the shared API and has a CLI equivalent; live events refresh visible state; deep links reopen the same instance; keyboard and narrow-screen operation are usable. No Svelte runtime is introduced. |

The UI should borrow Kata's useful interaction patterns: a collection with filters and selectable columns, a focused detail panel, bookmarkable URL state, and narrow-screen priority for the list ([Kata Web UI](https://github.com/kenn-io/kata/blob/0e8c930/docs/guide/web-ui.md#L43-L111)). Devproxy's primary views should instead answer local operations questions: which worktree owns this URL, which services are healthy, what Caddy route is applied, and what needs attention. The design should use Devproxy's own visual language.

These phases describe a direction, not implemented behavior. Kata features cited from its `main` branch may be newer than its latest release; verify installed CLI capabilities before building an integration.
