# AGENTS.md

windforce-core — a small source-sync runtime and execution engine for
Windforce-style apps. Go + PostgreSQL queue; TypeScript, Python, and Go
actions deployed from git.

This file is the canonical agent guide for this repository. `CLAUDE.md` only
imports it — rules live in one place.

## Identity and scope

windforce-core is a **self-sufficient execution engine**: source sync →
catalog/releases → run/job queue → execution. A self-hoster bringing their own
workers must be able to run everything in this repository with no external
service.

The scope discipline of [docs/adr/0001-scope.md](docs/adr/0001-scope.md)
applies to all changes:

- **In scope** (execution semantics): source sync, catalog and release
  publishing, the run/job queue, the ctx-first `main(ctx)` execution contract,
  the worker matching protocol (labels, worker registration, claim, heartbeat),
  outbound webhooks, the execution API and SDKs, the embedded admin UI.
- **Out of scope** (belongs to downstream products and adapters): account and
  multi-tenant SaaS management, billing and quota, managed worker fleets and
  autoscaling, product consoles beyond the embedded UI, product-specific
  vocabulary and integrations.

Litmus test for any new feature: **"does a self-hoster need this?"** If yes,
it may belong here. If it only makes sense for a hosted commercial service, it
does not — keep the engine generic and let adapters or downstream products own
it.

## Vocabulary

Neutral engine vocabulary only: App, Action, Run/Job, Worker, Release,
Workspace. Do not introduce product or brand vocabulary into engine code,
APIs, or docs. Adapters may map external vocabulary onto App/Action, never the
other way around.

Workspace is an organizational scoping partition inside one engine instance,
not a tenant isolation boundary — do not design features that treat it as one.
Tenant isolation is obtained by running one engine instance per tenant.

## Decisions

Engine contract decisions are recorded as public ADRs in
[docs/adr/](docs/adr/). Add an ADR when changing execution semantics, the
`windforce.json` manifest, HTTP API surfaces, the webhook contract, or the
worker protocol. General docs describe the current contract only; history and
rationale live in ADRs.

## Workflow

- Every commit is signed off under the DCO: `git commit -s`. See
  [CONTRIBUTING.md](CONTRIBUTING.md).
- Verify before submitting: `make fmt`, `make build`, `make test`; for web UI
  changes also `make web-test` and `make web-typecheck`.
- Conventional commit style: `feat: ...`, `fix: ...`, `docs: ...`.
- Releases are SemVer `v*` tags; pre-1.0 minor releases may break.
- Never commit secrets, tokens, internal endpoints, or local state
  (`.windforce-core/`, `.env`).
