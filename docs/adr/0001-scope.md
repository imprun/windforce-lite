# ADR 0001: windforce-lite scope

## Status

Accepted

## Context

Full Windforce includes a broad product surface: API, worker orchestration,
console UI, source synchronization, object cache, catalog, scheduling, policy,
and language runtimes.

`windforce-lite` keeps only the source deployment and JSON execution path. It is
meant to be small enough to embed, fork, or adapt without adopting the full
platform.

## Decision

The project uses public Windforce terms only:

- App
- Action
- Deployment
- Catalog
- Bundle
- Job

Product-specific terms stay in adapters. Adapters may map an external route or
request vocabulary to App/Action, but they must not change the core
catalog/runtime contract.

The MVP components are:

- `windforce.json` app manifest
- source-only bundle store
- active deployment catalog
- git/local sync
- bundle fetch before execution
- JSON subprocess runner

## Consequences

The first usable version is intentionally a local-first binary. A production
installation can later swap the local bundle store and file catalog for S3,
MinIO, SQL, or another control-plane backend without changing the app/action
execution contract.
