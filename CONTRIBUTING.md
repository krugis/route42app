# Contributing to Route42 Community Edition

Thanks for your interest in contributing! This document covers the essentials.

## Developer Certificate of Origin (DCO)

We use the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a CLA. Every commit must be signed off:

```bash
git commit -s -m "your message"
```

This adds a `Signed-off-by: Your Name <you@example.com>` line certifying that
you have the right to submit the contribution under the Apache-2.0 license.
Pull requests with unsigned commits cannot be merged.

## Development setup

Requirements: Go 1.22+ (no other services needed — Route42 is a single binary
with embedded SQLite).

```bash
git clone https://github.com/krugis/route42app
cd route42app
go build ./...
go test ./...
```

Before opening a PR, make sure the full check suite passes:

```bash
go build ./... && go vet ./... && go test -race ./...
```

End-to-end tests that require a running Ollama instance are gated behind a
build tag and are optional locally:

```bash
go test -tags e2e ./test/e2e/...
```

## Updating the model catalog

The model catalog lives in `data/catalog.json` and is a versioned snapshot —
we welcome PRs that fix or update entries:

- Keep changes factual and sourced: link the provider's pricing/model page in
  the PR description.
- Do not change the schema; schema changes need an issue first.
- One provider per PR where possible, to keep review simple.

## Pull request guidelines

- Open an issue first for new features or behavior changes; small fixes can go
  straight to a PR.
- Add or update tests for any behavior you change. Ranking and analyzer changes
  must keep the golden tests deterministic.
- Keep the public API (`/api/*` request/response shapes) backward compatible;
  breaking changes need discussion in an issue.
- Note for scope: ML-trained analyzers, behavioral learning, and the catalog
  update pipeline are part of Route42 Pro and are out of scope for this
  repository.

## Reporting security issues

Please do not open public issues for security vulnerabilities. Email
security@route42.app instead.
