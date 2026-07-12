# ADR 0005: Aggregate-first job observability in the Web UI

## Status

Accepted. Amends the job-inspection scope of [ADR 0003](0003-lightweight-admin-ui.md)
and the Jobs screens introduced by [ADR 0004](0004-web-ui-rewrite.md).

## Context

The first version of the rebuilt Web UI treated per-job inspection as a
first-class area: a paginated job list, a job detail page with the recorded
input, the result envelope, a log tail, and a per-action test-run form.

That design assumes an operator reads individual runs. At production scale —
millions of jobs — that assumption is wrong: nobody pages through job rows,
and per-record browsing answers no operational question. What matters is the
shape of the workload: how much is queued, what is running, and where the
failure rate is moving, per app and per route tag.

## Decision

- The Web UI's Jobs area is an **aggregate activity dashboard** built on
  `GET /jobs/summary`: workspace totals plus per-app and per-tag breakdowns
  (queued, running, completed/failed/canceled in a selectable recent window,
  and the derived failure rate).
- The Web UI does **not** offer per-job browsing: no job list, no job detail,
  no input/result/log viewers, and no cancel button. Automation and debugging
  keep using the control-plane API (`/jobs`, `/jobs/{id}`, logs, cancel) and
  the CLI, which remain unchanged.
- The per-action **test-run form is removed**. The Actions tab shows the
  materialized schemas; invoking actions is an API/CLI concern, not an admin
  UI concern.
- App detail readiness keeps using the same summary aggregates (per-route-tag
  activity), so the UI reads one consistent source.

## Non-goals

- Removing or changing server-side job recording, retention, or the per-job
  API surface. This ADR is about what the admin UI surfaces, not what the
  control plane stores.
- Time-series/metrics infrastructure. The dashboard reads the existing
  summary endpoint; exporting metrics to a real monitoring stack stays out of
  scope for the lite UI.

## Consequences

- The UI stays responsive regardless of job volume: every Jobs-area request
  is one summary call, never a scan of job rows.
- Operators diagnose *which app/tag* is unhealthy in the UI, then drill into
  individual runs with the API/CLI when they truly need payloads or logs.
- The web client drops the job-list pagination and job-detail polling code
  paths entirely, which removes the hardest client-side state management in
  the UI.
