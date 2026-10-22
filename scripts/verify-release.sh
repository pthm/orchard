#!/bin/sh
# Verify release prerequisites before goreleaser runs.
# Called by goreleaser's before hook with the release tag, e.g.
#   ./scripts/verify-release.sh {{ .Tag }}
set -e

version="$1"

echo "Verifying release prerequisites..."

# In snapshot (dry-run) mode {{ .Tag }} is whatever `git describe` finds, or
# v0.0.0 with no tags, so it is not guaranteed to be a release tag. Only
# enforce the format on a real release.
if [ "${DRY_RUN:-false}" = "true" ]; then
  echo "  dry run: skipping tag format check (tag: ${version:-<none>})"
else
  if [ -z "$version" ]; then
    echo "ERROR: release version not passed (goreleaser before hook must pass the tag)"
    exit 1
  fi
  if ! echo "$version" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$'; then
    echo "ERROR: version must look like v1.2.3 or v1.2.3-rc.1, got: $version"
    exit 1
  fi
  echo "  release version: $version"
fi

# A replace directive in the root go.mod breaks `go install pkg@version` for
# consumers. Guard in every mode.
if grep -qE '^replace ' go.mod; then
  echo "ERROR: root go.mod has replace directive(s) — breaks 'go install github.com/pthm/orchard@latest':"
  grep -E '^replace ' go.mod
  exit 1
fi
echo "  no replace directives in go.mod (go install safe)"

echo "All release prerequisites verified."
