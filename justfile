# Build the dashboard and standalone devproxy binary.
build: web
    go build -o bin/devproxy ./cmd/devproxy

# Install locked frontend dependencies and regenerate embedded assets.
web:
    npm ci --prefix web
    npm run build --prefix web

# Build from the checked-in dashboard assets without Node.js.
go:
    go build -o bin/devproxy ./cmd/devproxy

# Run the Go test suite.
test:
    go test ./...

# Start Caddy in Docker Desktop bridge mode and run the daemon.
serve-desktop: go
    docker compose -f caddy/compose.desktop.yml up -d
    bin/devproxy serve --docker-desktop
