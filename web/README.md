# Dashboard frontend

The dashboard is a read-only view of the daemon's registrations. It uses React, Chakra UI, TanStack Query, Form, and Table. The CLI remains the way to start and register services.

From this directory, run `npm ci` and `npm run build` after changing the frontend. Vite writes production files to `internal/devproxy/web/dist`; those files are checked in and embedded by Go. A fresh checkout can still run `go build ./cmd/devproxy` without Node.js.

For local frontend work, start `devproxy serve` and run `npm run dev`. Vite proxies `/v1` to `127.0.0.1:7654`.
