# CI/CD Workflows

This directory contains the GitHub Actions workflows used by the project.

## Workflows

### `ci.yml` — main CI pipeline

- **Triggers**: pull requests and pushes to `main`
- **Jobs**:
  - **Test**: runs `go test -race` with coverage
  - **Build**: builds the server and client, then runs `govulncheck`
  - **Lint**: runs `go vet` and `golangci-lint`
  - **Docker**: builds the container image

### `test.yml` — manual test runner

- **Trigger**: `workflow_dispatch` only
- Runs the regular, race, and coverage test suites
- Useful when re-running tests without pushing a commit

### `release.yml` — build and publish a release

- **Triggers**: pushing a tag that starts with `v` (for example `v0.2.0`), or manual `workflow_dispatch`
- **Jobs**:
  - **Release**: cross-compiles the server for Linux, macOS, and Windows on amd64/arm64,
    creates `.tar.gz`/`.zip` archives with `README.md` and `LICENSE`, writes
    `checksums.txt`, and creates a GitHub Release with generated release notes
  - **Docker**: builds and pushes a multi-architecture image to
    `ghcr.io/n3ur051s/loki-mcp`
- Uses the default `GITHUB_TOKEN`; no additional secrets are required

To publish a release, push a tag:

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push github main --tags
```

## Transport Tests

The test suite starts the real HTTP handlers and verifies:

- Streamable HTTP initialization, `tools/list`, and `tools/call`
- The `/stream` backward-compatible alias
- Stateful session creation and `DELETE` termination
- CORS preflight handling
- Health and service-discovery endpoints
- Configuration validation and defaults

The handler tests also cover timestamp parsing and formatting regressions, including the original
year-2262 bug (issue #3).

## Running Tests Locally

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Run tests with race detection (requires cgo on some platforms)
go test -race ./...

# Build verification
go build ./cmd/server
go build ./cmd/client

# Build the container image
docker build -t loki-mcp-server:test .
```

## Adding New Tests

When adding new functionality:

1. Add unit tests next to the code in the appropriate `*_test.go` file.
2. Add a transport-level test in `cmd/server/main_test.go` when the change affects HTTP behavior.
3. Add a regression test for bug fixes.
4. Run `go vet ./...` and `go test ./...` before opening a pull request.

CI runs automatically for pull requests and pushes to `main`.
