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

# Build and install or update devproxy in a user-writable directory.
install: build
    #!/usr/bin/env bash
    set -euo pipefail
    install_dir="${DEVPROXY_INSTALL_DIR:-$HOME/.local/bin}"
    mkdir -p "$install_dir"
    temporary="$(mktemp "$install_dir/.devproxy.XXXXXXXX")"
    trap 'rm -f "$temporary"' EXIT
    install -m 755 bin/devproxy "$temporary"
    mv -f "$temporary" "$install_dir/devproxy"
    printf 'Installed %s\n' "$install_dir/devproxy"
    case ":$PATH:" in
      *":$install_dir:"*) ;;
      *) printf 'Add %s to PATH to run devproxy from any directory.\n' "$install_dir" ;;
    esac

# Run the Go test suite.
test:
    go test ./...

# Export the Cobra command tree as a usage specification.
usage-spec:
    go run ./cmd/devproxy --usage-spec

# Check the exported specification with the mise-managed usage CLI.
usage-lint:
    #!/usr/bin/env bash
    set -euo pipefail
    go run ./cmd/devproxy --usage-spec | mise exec -- usage lint -

# Start Caddy in Docker Desktop bridge mode and run the daemon.
serve-desktop: go
    docker compose -f caddy/compose.desktop.yml up -d
    bin/devproxy serve --caddy-admin http://127.0.0.1:2019 --docker-desktop
