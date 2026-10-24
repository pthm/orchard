# Contributing to Orchard

Issues and pull requests are welcome. Before proposing a change:

1. `just test` passes.
2. `go vet ./...` is clean.
3. New behaviour has a test. User-visible changes update the docs under
   `docs/content/`.

Provider authors, built-in or external, should read
[Extending Orchard](https://orchard.pthm.dev/docs/extending/) first: it covers the
wire protocol, lifecycle, schema model, and the conventions that plan, the docs
generator and external clients rely on.

## Development

Orchard uses [`just`](https://github.com/casey/just) as its task runner.

```sh
just                   # list recipes
just test              # unit tests
just test-all          # unit tests and the integration suite (needs Docker)
just docs              # regenerate provider docs and build the Hugo site
just docs-watch        # live-reload the docs site
just docs-provider p   # regenerate one provider's reference page
```

The integration suite starts Postgres, MySQL, Redis and SeaweedFS (for S3) in
containers with testcontainers.

The docs site pulls generated provider reference pages from
`docs/content-generated/`, which is gitignored. Run `just docs-generate` after
editing any `pkg/provider/*/schema.hcl`.

## Layout

```
cmd/               CLI subcommands
internal/          Parser, dependency graph, engine, run records, docs renderer
pkg/
  protocol/        Provider wire protocol and schema types
  provider/        Provider interface, builtin registry, external (stdio) client,
                   and the builtin providers, one package each
  dbschema/        Portable database schema model, for typed SQL actions
  snapshot/        Schema snapshot files (.orchard/schema/)
docs/              Hugo source for https://orchard.pthm.dev
test/integration/  End-to-end scenarios against real services
```

## Releasing

Push a `vX.Y.Z` tag (prereleases like `v1.2.3-rc.1` are allowed) and the `release`
workflow runs GoReleaser to publish archives, packages, the Homebrew cask and the
Scoop manifest. It needs these repository secrets:

- `HOMEBREW_TAP_GITHUB_TOKEN`: push access to `pthm/homebrew-tap` (the cask is
  skipped if unset).
- `SCOOP_BUCKET_GITHUB_TOKEN`: push access to `pthm/scoop-bucket` (the manifest
  is skipped if unset).
- `QUILL_SIGN_P12`, `QUILL_SIGN_PASSWORD`, `QUILL_NOTARY_KEY`,
  `QUILL_NOTARY_KEY_ID`, `QUILL_NOTARY_ISSUER`: macOS signing and notarization.

Run the workflow manually with `dry_run` to build a snapshot without publishing.
