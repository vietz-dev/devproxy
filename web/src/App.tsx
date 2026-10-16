import { useMemo, useState } from "react";
import { Badge, Button, HStack, Input, Spinner, Text } from "@chakra-ui/react";
import { useForm } from "@tanstack/react-form";
import { useQuery } from "@tanstack/react-query";
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type SortingState,
} from "@tanstack/react-table";
import {
  Activity,
  ArrowUpDown,
  Check,
  ChevronRight,
  CircleHelp,
  Copy,
  ExternalLink,
  GitBranch,
  Globe2,
  Network,
  RefreshCw,
  Search,
  Terminal,
} from "lucide-react";

type Service = {
  id: string;
  project: string;
  worktree: string;
  service: string;
  route: string;
  port: number;
  host: string;
  url: string;
  expires_at: string;
};

type Filter = { text: string; route: string };

async function loadServices(): Promise<Service[]> {
  const response = await fetch("/v1/services", { cache: "no-store" });
  if (!response.ok)
    throw new Error(`Service list returned HTTP ${response.status}`);
  return response.json() as Promise<Service[]>;
}

const column = createColumnHelper<Service>();
const columns = [
  column.accessor("service", {
    header: "SERVICE",
    cell: (info) => <span className="service-name">{info.getValue()}</span>,
  }),
  column.accessor("route", {
    header: "ROUTE",
    cell: (info) => <code className="route-code">{info.getValue()}</code>,
  }),
  column.accessor("url", {
    header: "ORIGIN",
    cell: (info) => (
      <span className="origin-cell">
        {info.getValue().replace("https://", "")}
      </span>
    ),
  }),
  column.accessor("port", {
    header: "PORT",
    cell: (info) => <span className="mono">:{info.getValue()}</span>,
  }),
  column.accessor("expires_at", {
    header: "LEASE",
    cell: (info) => <Lease value={info.getValue()} />,
  }),
];

function selectionFromURL(): string {
  return new URLSearchParams(window.location.search).get("service") ?? "";
}

function scopeFromURL(): string {
  return new URLSearchParams(window.location.search).get("scope") ?? "all";
}

function Lease({ value }: { value: string }) {
  const seconds = Math.max(
    0,
    Math.ceil((Date.parse(value) - Date.now()) / 1000),
  );
  return (
    <span className={seconds > 0 ? "lease-active" : "lease-expired"}>
      <span className="status-dot" />
      {seconds > 0 ? `${seconds}s left` : "Expiring"}
    </span>
  );
}

function App() {
  const {
    data = [],
    error,
    isPending,
    isFetching,
    refetch,
    dataUpdatedAt,
  } = useQuery({
    queryKey: ["services"],
    queryFn: loadServices,
    refetchInterval: 5000,
  });
  const [scope, setScope] = useState(scopeFromURL);
  const [selectedID, setSelectedID] = useState(selectionFromURL);
  const [filter, setFilter] = useState<Filter>({ text: "", route: "" });
  const [sorting, setSorting] = useState<SortingState>([]);
  const [copied, setCopied] = useState(false);
  const form = useForm({
    defaultValues: { text: "", route: "" },
    onSubmit: ({ value }) =>
      setFilter({
        text: value.text.trim().toLowerCase(),
        route: value.route.trim(),
      }),
  });
  const projects = useMemo(() => {
    const grouped = new Map<string, Map<string, number>>();
    for (const item of data) {
      const worktrees = grouped.get(item.project) ?? new Map<string, number>();
      worktrees.set(item.worktree, (worktrees.get(item.worktree) ?? 0) + 1);
      grouped.set(item.project, worktrees);
    }
    return [...grouped.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [data]);
  const visible = useMemo(
    () =>
      data.filter((item) => {
        if (scope !== "all" && scope !== `${item.project}/${item.worktree}`)
          return false;
        if (filter.route && !item.route.startsWith(filter.route)) return false;
        if (
          filter.text &&
          !`${item.project} ${item.worktree} ${item.service} ${item.route} ${item.url}`
            .toLowerCase()
            .includes(filter.text)
        )
          return false;
        return true;
      }),
    [data, scope, filter],
  );
  const table = useReactTable({
    data: visible,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });
  const selected = data.find((item) => item.id === selectedID);
  const title = scope === "all" ? "All services" : scope.replace("/", " / ");

  function selectService(id: string) {
    setSelectedID(id);
    const next = new URL(window.location.href);
    if (id) next.searchParams.set("service", id);
    else next.searchParams.delete("service");
    window.history.replaceState(null, "", next);
  }

  function selectScope(nextScope: string) {
    setScope(nextScope);
    const next = new URL(window.location.href);
    if (nextScope === "all") next.searchParams.delete("scope");
    else next.searchParams.set("scope", nextScope);
    window.history.replaceState(null, "", next);
  }

  async function copy(value: string) {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark">
            <Network size={18} strokeWidth={2.2} />
          </div>
          <span>devproxy</span>
          <span className="brand-divider" />
          <span className="brand-subtitle">WORKSPACE</span>
        </div>
        <div className="top-actions">
          <span className={`local-pill ${error ? "offline" : ""}`}>
            <span className="status-dot" />{" "}
            {error
              ? "CONNECTION ERROR"
              : isPending
                ? "CONNECTING"
                : "LOCAL DAEMON"}
          </span>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => void refetch()}
            aria-label="Refresh services"
          >
            <RefreshCw size={15} className={isFetching ? "spin" : ""} />
          </Button>
        </div>
      </header>
      <div className="workspace">
        <aside className="sidebar" aria-label="Project navigation">
          <div className="nav-heading">WORKSPACE</div>
          <button
            className={`nav-item ${scope === "all" ? "active" : ""}`}
            onClick={() => selectScope("all")}
          >
            <Globe2 size={16} />
            All services<span className="nav-count">{data.length}</span>
          </button>
          <div className="nav-heading projects-heading">PROJECTS</div>
          {projects.map(([project, worktrees]) => (
            <div className="project-group" key={project}>
              <div className="project-label">
                <ChevronRight size={13} />
                {project}
                <span className="nav-count">
                  {[...worktrees.values()].reduce((a, b) => a + b, 0)}
                </span>
              </div>
              {[...worktrees.entries()]
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([worktree, count]) => (
                  <button
                    key={worktree}
                    className={`nav-item worktree-item ${scope === `${project}/${worktree}` ? "active" : ""}`}
                    onClick={() => selectScope(`${project}/${worktree}`)}
                  >
                    <GitBranch size={14} />
                    {worktree}
                    <span className="nav-count">{count}</span>
                  </button>
                ))}
            </div>
          ))}
          {projects.length === 0 && (
            <p className="sidebar-empty">
              Projects appear when a service registers.
            </p>
          )}
          <div className="sidebar-foot">
            <CircleHelp size={15} />
            <span>
              Start a service with <code>devproxy run</code>
            </span>
          </div>
        </aside>
        <main className="main-content">
          <div className="page-top">
            <div>
              <div className="eyebrow">ROUTING / OVERVIEW</div>
              <h1>{title}</h1>
              <p>Local HTTPS origins for services in your Git worktrees.</p>
            </div>
            <div className="summary-count">
              <strong>{visible.length}</strong>
              <span>ACTIVE ROUTES</span>
            </div>
          </div>
          <div className="mobile-scope">
            <label htmlFor="worktree-scope">Worktree</label>
            <select
              id="worktree-scope"
              value={scope}
              onChange={(event) => selectScope(event.target.value)}
            >
              <option value="all">All services</option>
              {projects.map(([project, worktrees]) =>
                [...worktrees.keys()]
                  .sort((a, b) => a.localeCompare(b))
                  .map((worktree) => (
                    <option
                      key={`${project}/${worktree}`}
                      value={`${project}/${worktree}`}
                    >
                      {project} / {worktree}
                    </option>
                  )),
              )}
            </select>
          </div>
          <div className="content-grid">
            <section className="list-panel" aria-label="Registered services">
              <div className="panel-title">
                <HStack gap="2">
                  <Activity size={17} />
                  <Text fontWeight="semibold">Registered services</Text>
                </HStack>
                <span className="update-time">
                  {dataUpdatedAt
                    ? `Updated ${new Date(dataUpdatedAt).toLocaleTimeString()}`
                    : "Connecting…"}
                </span>
              </div>
              <form
                className="filters"
                onSubmit={(event) => {
                  event.preventDefault();
                  void form.handleSubmit();
                }}
              >
                <form.Field name="text">
                  {(field) => (
                    <label className="search-field">
                      <Search size={15} />
                      <Input
                        size="sm"
                        name={field.name}
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(event) =>
                          field.handleChange(event.target.value)
                        }
                        placeholder="Search services, worktrees, URLs"
                        aria-label="Search services"
                      />
                    </label>
                  )}
                </form.Field>
                <form.Field name="route">
                  {(field) => (
                    <Input
                      className="route-filter"
                      size="sm"
                      name={field.name}
                      value={field.state.value}
                      onBlur={field.handleBlur}
                      onChange={(event) =>
                        field.handleChange(event.target.value)
                      }
                      placeholder="Route prefix"
                      aria-label="Route prefix"
                    />
                  )}
                </form.Field>
                <Button size="sm" type="submit" colorPalette="blue">
                  Filter
                </Button>
                {(filter.text || filter.route) && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      form.reset();
                      setFilter({ text: "", route: "" });
                    }}
                  >
                    Clear
                  </Button>
                )}
              </form>
              {error && (
                <div className="notice error" role="alert">
                  Could not load registrations: {(error as Error).message}
                </div>
              )}
              {isPending && (
                <div className="empty">
                  <Spinner size="sm" /> Loading services…
                </div>
              )}
              {!isPending && !error && visible.length === 0 && (
                <div className="empty">
                  <Terminal size={24} />
                  <strong>No matching services</strong>
                  <span>
                    {data.length
                      ? "Try a different project or filter."
                      : "Run devproxy run --project NAME SERVICE -- COMMAND to register one."}
                  </span>
                </div>
              )}
              {visible.length > 0 && (
                <div className="table-scroll">
                  <table>
                    <thead>
                      {table.getHeaderGroups().map((group) => (
                        <tr key={group.id}>
                          {group.headers.map((header) => (
                            <th key={header.id}>
                              <button
                                onClick={header.column.getToggleSortingHandler()}
                              >
                                {flexRender(
                                  header.column.columnDef.header,
                                  header.getContext(),
                                )}
                                <ArrowUpDown size={11} />
                              </button>
                            </th>
                          ))}
                        </tr>
                      ))}
                    </thead>
                    <tbody>
                      {table.getRowModel().rows.map((row) => (
                        <tr
                          key={row.id}
                          className={
                            selectedID === row.original.id ? "selected" : ""
                          }
                          onClick={() => selectService(row.original.id)}
                        >
                          {row.getVisibleCells().map((cell) => (
                            <td key={cell.id}>
                              {cell.column.id === "service" ? (
                                <button
                                  className="row-select"
                                  aria-label={`Inspect ${row.original.service} in ${row.original.project} / ${row.original.worktree}`}
                                  aria-pressed={selectedID === row.original.id}
                                  onClick={() => selectService(row.original.id)}
                                >
                                  {flexRender(
                                    cell.column.columnDef.cell,
                                    cell.getContext(),
                                  )}
                                </button>
                              ) : (
                                flexRender(
                                  cell.column.columnDef.cell,
                                  cell.getContext(),
                                )
                              )}
                            </td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="table-foot">
                Registrations renew while their service is running. The list
                refreshes every 5 seconds.
              </div>
            </section>
            <aside className="detail-panel" aria-label="Service details">
              {selected ? (
                <>
                  <div className="detail-head">
                    <div className="detail-icon">
                      <Network size={20} />
                    </div>
                    <Badge colorPalette="green" variant="subtle">
                      REGISTERED
                    </Badge>
                  </div>
                  <div className="eyebrow">SERVICE DETAIL</div>
                  <h2>{selected.service}</h2>
                  <p className="detail-subtitle">
                    {selected.project} / {selected.worktree}
                  </p>
                  <div className="detail-divider" />
                  <div className="detail-label">HTTPS ORIGIN</div>
                  <a
                    className="detail-url"
                    href={selected.url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {selected.url}
                    <ExternalLink size={14} />
                  </a>
                  <div className="detail-actions">
                    <Button
                      size="sm"
                      colorPalette="blue"
                      onClick={() =>
                        window.open(
                          selected.url,
                          "_blank",
                          "noopener,noreferrer",
                        )
                      }
                    >
                      Open service <ExternalLink size={14} />
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => void copy(selected.url)}
                    >
                      {copied ? <Check size={14} /> : <Copy size={14} />}
                      {copied ? "Copied" : "Copy URL"}
                    </Button>
                  </div>
                  <div className="detail-divider" />
                  <div className="detail-label">ROUTE</div>
                  <div className="detail-value">
                    <code>{selected.route}</code>
                    <span>Path prefix</span>
                  </div>
                  <div className="detail-label">UPSTREAM</div>
                  <div className="detail-value">
                    <code>127.0.0.1:{selected.port}</code>
                    <span>Local port</span>
                  </div>
                  <div className="detail-label">REGISTRATION LEASE</div>
                  <div className="detail-value">
                    <Lease value={selected.expires_at} />
                    <span>Renews automatically</span>
                  </div>
                  <div className="detail-note">
                    Registration confirms a route is advertised. It does not
                    check the upstream service’s HTTP health.
                  </div>
                </>
              ) : (
                <div className="detail-empty">
                  <div className="detail-icon">
                    <Network size={20} />
                  </div>
                  <h2>Service details</h2>
                  <p>
                    Select a route to inspect its origin, upstream port, and
                    lease.
                  </p>
                </div>
              )}
            </aside>
          </div>
          <footer className="footer">
            <span>devproxy routes traffic through Caddy</span>
            <span>CLI FIRST · LOCAL DEVELOPMENT</span>
          </footer>
        </main>
      </div>
    </div>
  );
}

export default App;
