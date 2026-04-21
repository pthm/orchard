# Orchard task runner. Invoke with `just <recipe>`.

# List available recipes.
default:
    @just --list

# Regenerate docs pages from schemas and the function registry.
docs-generate:
    mkdir -p docs/content-generated/docs/providers
    mkdir -p docs/content-generated/docs/reference
    go run . docs builtin/postgres > docs/content-generated/docs/providers/postgres.md
    go run . docs builtin/http     > docs/content-generated/docs/providers/http.md
    go run . docs builtin/exec     > docs/content-generated/docs/providers/exec.md
    go run . functions --hugo      > docs/content-generated/docs/reference/functions.md

# Generate one provider page. Useful while iterating.
docs-provider name:
    mkdir -p docs/content-generated/docs/providers
    go run . docs builtin/{{name}} > docs/content-generated/docs/providers/{{name}}.md

# Build the docs site. Generates provider pages first.
docs: docs-generate
    cd docs && hugo --minify

# Live preview of the docs site. Regenerate provider pages first;
# schema edits require re-running `just docs-generate` manually.
docs-watch: docs-generate
    cd docs && hugo server --disableFastRender

# Format Go code with gofumpt.
fmt:
    go tool gofumpt -w .

# Lint Go code with golangci-lint.
lint:
    go tool golangci-lint run ./...

# Run the Go test suite.
test:
    go test ./...

# Run the Go tests plus the integration suite.
test-all:
    go test ./...
    cd test/integration && go test ./...

# Run all checks (fmt, lint, test).
check: fmt lint test
