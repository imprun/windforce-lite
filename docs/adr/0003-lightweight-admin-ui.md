# ADR 0003: Lightweight admin Web UI

## Status

Accepted

## Context

`windforce-lite` needs a small operator-facing UI for source and deployment
operations. This is different from rebuilding the full Windforce console.

The UI belongs in the core repository when it manages Windforce-lite-owned
concepts: git source registration, source sync, app/action deployment, active
catalog state, deployment history, and run status.

Product-specific compatibility surfaces belong in separate adapter repositories.
The Web UI must not embed adapter-specific request vocabulary or envelope
semantics.

## Decision

The admin Web UI is in scope with these capabilities:

- register, view, and disable git sources
- trigger source sync for a registered source
- deploy an app/action catalog entry from a synced commit
- show active deployment per app
- show deployment history with commit, source id, actor, timestamp, and status
- roll back to a previous materialized deployment
- inspect run status, job result, and HITL state

The UI uses the same core control-plane API as CLI/API clients. It must not have
a separate source of truth.

## Non-goals

- full Windforce console parity
- SaaS tenant management
- billing, quota, marketplace, or scheduler UI
- workflow designer
- product-specific adapter administration
- direct editing of action source code

## Consequences

Deployment history becomes a first-class core concept. The runtime still executes
pinned deployments, and workers still fetch source by workspace/git-source/commit
without git credentials.

The UI can be served by the core API process or by a static frontend pointed at
the core API, but it must remain deployable without product-specific adapters.
