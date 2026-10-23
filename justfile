# Orchard task runner. Invoke with `just <recipe>`.

# List available recipes.
default:
    @just --list

# Regenerate docs pages from schemas and the function registry.
docs-generate:
    mkdir -p docs/content-generated/providers
    mkdir -p docs/content-generated/reference
    go run . docs builtin/postgres > docs/content-generated/providers/postgres.md
    go run . docs builtin/sqlite   > docs/content-generated/providers/sqlite.md
    go run . docs builtin/mysql    > docs/content-generated/providers/mysql.md
    go run . docs builtin/redis    > docs/content-generated/providers/redis.md
    go run . docs builtin/s3       > docs/content-generated/providers/s3.md
    go run . docs builtin/grpc     > docs/content-generated/providers/grpc.md
    go run . docs builtin/http     > docs/content-generated/providers/http.md
    go run . docs builtin/exec     > docs/content-generated/providers/exec.md
    go run . functions --hugo      > docs/content-generated/reference/functions.md

# Generate one provider page. Useful while iterating.
docs-provider name:
    mkdir -p docs/content-generated/providers
    go run . docs builtin/{{name}} > docs/content-generated/providers/{{name}}.md

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

# Regenerate the gRPC provider's test descriptor set after editing
# accounts.proto.
grpc-testdata:
    cd pkg/provider/grpc/grpctest/testdata && protoc --include_imports --include_source_info --descriptor_set_out=accounts.binpb accounts.proto
