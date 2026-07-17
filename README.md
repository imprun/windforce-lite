# windforce-core

`windforce-core` is the Windforce Lite control plane and execution runtime for
apps described by `windforce.json`.

It keeps the useful core of Windforce:

- register a repository source
- materialize an exact source snapshot by workspace, git source id, and commit
- prepare and publish a content-addressed execution bundle
- select an active app release in a catalog
- track deployment history
- pin the active release into each Run and Job
- fetch the pinned execution bundle before execution
- run the app entrypoint with `main(ctx)` and dispatch by action

It intentionally does not include the full Windforce product surface:
multi-tenant SaaS concerns, quota, scheduler, billing, or an operator. A small
admin Web UI for git source registration, deployment, and deployment history is
in scope.

## Concepts

- Repository source: the Git location, branch, subpath, and credential reference
- App: the stable executable identity declared in `windforce.json`
- Action: one executable unit inside an app
- Synchronized revision: an exact validated source commit available for release
- Deployment: the app, commit, bundle digest, and executable action metadata
- Release: an immutable published deployment; one release is active for new Runs
- Catalog: the active deployment index stored in the selected state backend
- Source Store: source-only object cache keyed by workspace/git-source/commit
- Execution Artifact Store: prepared immutable bundles keyed by SHA-256 digest
- Worker-local cache: a disposable fetched copy of a pinned execution bundle
- Deployment history: an audit trail of published releases

Read [Core concepts](docs/concepts/core-concepts.md) for the exact storage,
fingerprint, marker, Run, and Job definitions. The complete state flow is in
[Release and execution lifecycle](docs/concepts/release-lifecycle.md).

## Deploy

`sync` stores the latest valid source revision. `deploy` prepares that revision
for the worker runtime and publishes it as the active release used by new jobs.

1. Register a git source through the control-plane API. Registration validates
   repository access, branch existence, subpath containment, `windforce.json`,
   action schemas, and lockfile reproducibility before saving the source.
2. Sync the source to resolve an exact commit and validate its manifest,
   schemas, and lockfile.
3. If the git source has a `subpath`, use that repo directory as the app root
   and try sparse checkout before falling back to a full clone.
4. Materialize the source tree into the bundle store under
   `{workspace}/{gitSourceId}/{commit}`.
5. Record the source as the latest synchronized revision. This does not install
   dependencies or change the active release.
6. Deploy pins the latest synchronized revision while holding the source
   operation lock.
7. Prepare the exact source with the runtime contract used by workers. Python
   dependencies, Bun lockfiles, Go builds, and the entrypoint must validate.
   Store the result as a content-addressed execution bundle.
8. Publish only the validated execution bundle. The active release, release
   history, audit record, Control Plane event, and matching Webhook deliveries
   are written in one state-store transaction.

The ordering is intentional: synchronization can succeed independently of the
runtime toolchain, while a failed deployment leaves the active release
unchanged. Webhook HTTP requests are not made in the publication transaction.

The state backend is the source of truth for the active release catalog. Local
mode stores it in the state JSON file; PostgreSQL mode stores it in control-plane
tables. `--catalog` names an optional catalog snapshot that is imported
idempotently at startup.

The Docker Compose control plane maps its API to `127.0.0.1:18091`. The
execution API is a separate service mapped to `127.0.0.1:18092`. The local Web UI is a Vite development server
(run with Bun) on `127.0.0.1:18090/ui/` and proxies control-plane API calls to
the backend. Local development uses `tools/windforce_control.py` against the
API instead of a separate source-sync command.

The Web UI is live during local development. Run `make web-dev` for a host dev
server, or `make compose-up` for the Compose-managed dev server. The production
UI is a static Vite build embedded into the Go binary: `make web-embed`
refreshes `internal/webui/assets` from `web/dist`, and the Dockerfile performs
the same build inside the image so the embedded assets are always rebuilt from
source. The API process serves the UI at `/ui/` with an SPA fallback for
client-side routes. See [ADR 0004](docs/adr/0004-web-ui-rewrite.md).

## Docker Compose profiles

The Compose file keeps PostgreSQL, the control plane, the execution API, and the
worker as separate processes.

```bash
docker compose --profile standalone up -d
docker compose --profile pg up -d postgres
docker compose --profile backend up -d control-plane execution-api
docker compose --profile worker up -d worker
```

`standalone` starts PostgreSQL, backend, and worker together. External protocol
adapters join the Compose network and call the backend's versioned Execution
API. PostgreSQL and the active catalog remain private to Windforce Core. The
compose `volume-init` service fixes the mounted data volume ownership before the
control-plane or worker starts.

## Run

A run request is admitted and executed as follows:

1. Resolve the active app deployment and requested action.
2. Pin the deployment, action schemas, routing, and execution settings into a
   Run and Job in one state-store transaction.
3. Fetch the pinned execution bundle digest from the Execution Artifact Store
   into the worker-local bundle cache.
4. Verify the bundle digest and preparation fingerprint, then execute the
   app-level entrypoint from the fetched bundle.
5. Build the Windforce `ctx` object from `input.json` and `WF_*` environment.
6. Store stdout/stderr as job logs and expose the action output through the
   job result API.

## Manifest

Every app source has a `windforce.json` file:

```json
{
  "app": "echo",
  "entrypoint": "main.py",
  "scriptLang": "python",
  "timeout": 30,
  "actions": {
    "echo": {
      "inputSchema": "input.schema.json",
      "outputSchema": "output.schema.json"
    }
  }
}
```

`entrypoint`, `scriptLang`, and `timeout` follow the canonical Windforce
manifest shape. `entrypoint` and `scriptLang` are app-level; actions branch
inside that entrypoint. `timeout` is the app default and an action may override
it with its own `timeout` in seconds. Source manifests do not declare action
commands or adapters; integration adapters live outside the app source contract.
The execution bundle builder supports canonical `typescript`, `python`, and
`go` entrypoints. Other `scriptLang` values use the Bun preparation path and
must pass entrypoint validation before a release is published.

Deployment prepares a content-addressed execution bundle for TypeScript,
Python, and Go entrypoints. It pins the latest synchronized commit, installs declared
dependencies, injects the matching Windforce SDK, validates the entrypoint, and
stores the complete prepared tree under its SHA-256 digest. TypeScript uses
`bun install --frozen-lockfile --no-progress`, Python installs into
`.windforce/site-packages`, and Go compiles the generated wrapper plus author
code into `.windforce/bin`.

Release publication only activates a synchronized revision whose execution
bundle was prepared successfully and matches its digest. A worker fetches that
immutable bundle, verifies that its preparation fingerprint is compatible with
the worker, and executes it. Job processing does not clone Git repositories,
install packages, or compile app source.

## Entrypoint contract

The executor writes `input.json` in a per-job directory, builds `ctx` from
`input.json` plus `WF_*`, imports the app entrypoint, calls `main(ctx)`, and
expects `result.json`. A non-zero process exit is returned as an action result,
not as a runner infrastructure error.

## Try it locally

Run the combined local control-plane and worker:

```powershell
go run ./cmd/windforce-core standalone --dev `
  --addr 127.0.0.1:8080 `
  --store .tmp/store `
  --state .tmp/state.json
```

In another terminal, use the control-plane API to create a managed sample git
source, sync it, enqueue a job, and read the result:

```powershell
python tools/windforce_control.py --api-url http://127.0.0.1:8080 --pretty sample --app-key sample_hello

Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/api/w/default/jobs/run/sample_hello/echo/wait?timeout_ms=5000 `
  -ContentType application/json `
  -Body '{"message":"hello"}'
```

## Local runtime mode

The runtime process model is available through local file-backed state:

```powershell
go run ./cmd/windforce-core standalone --dev `
  --addr :8080 `
  --store .tmp/store `
  --state .tmp/state.json
```

Enqueue an action through the canonical control-plane HTTP API:

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/api/w/default/jobs/run/echo/echo `
  -ContentType application/json `
  -Body '{"message":"hello"}'
```

Separated local processes use the same state file and bundle store:

```powershell
go run ./cmd/windforce-core control-plane --addr :8081 --state .tmp/state.json --store .tmp/store
go run ./cmd/windforce-core execution-api --addr :8082 --state .tmp/state.json --store .tmp/store
go run ./cmd/windforce-core worker --state .tmp/state.json --store .tmp/store
```

`worker --once` claims at most one queued job and exits, which is useful in
tests and scripted smoke checks.
`worker --tags default,app-blue` restricts claims to those pinned route tags;
when omitted, the worker claims every queued tag for simple local development.
`worker --log-flush-interval 2s --log-cap-bytes 20971520` matches the canonical
default of incremental job log flushing with a 20 MiB per-job cap; set
`--log-cap-bytes 0` only for local debugging.

Implemented control-plane endpoints:

- `GET /api/w/{workspace}/openapi.json` (workspace control-plane OpenAPI)
- `GET /api/w/{workspace}/git_sources`
- `POST /api/w/{workspace}/git_sources`
- `POST /api/w/{workspace}/git_sources/probe`
- `POST /api/w/{workspace}/git_sources/sample`
- `PATCH /api/w/{workspace}/git_sources/{gitSourceId}`
- `DELETE /api/w/{workspace}/git_sources/{gitSourceId}`
- `POST /api/w/{workspace}/git_sources/{gitSourceId}/sync`
- `POST /api/w/{workspace}/git_sources/{gitSourceId}/deploy`
- `GET /api/w/{workspace}/git_sources/{gitSourceId}/audit` (configuration change audit trail)
- `GET /api/w/{workspace}/apps`
- `GET /api/w/{workspace}/apps?view=summary`
- `GET /api/w/{workspace}/apps/{app}`
- `PATCH /api/w/{workspace}/apps/{app}`
- `POST /api/w/{workspace}/apps/{app}/requeue`
- `GET /api/w/{workspace}/apps/{app}/source`
- `GET /api/w/{workspace}/apps/{app}/history`
- `GET /api/w/{workspace}/apps/{app}/openapi.json` (app invocation OpenAPI generated from materialized action schemas)
- `GET /api/w/{workspace}/apps/{app}/actions/{action}` (canonical action detail including base64-encoded materialized `input_schema` and `output_schema`, matching Windforce catalog action JSON encoding)
- `GET /api/w/{workspace}/apps/{app}/actions/{action}/schema` (materialized action schemas as raw JSON Schema documents)
- `PATCH /api/w/{workspace}/apps/{app}/actions/{action}`
- `GET|PUT|DELETE /api/w/{workspace}/apps/{app}/input-configs` (app/action/client input settings)
- `GET /api/w/{workspace}/apps/{app}/input-config-audit`
- `GET /api/w/{workspace}/clients/{clientId}/input-configs`
- `GET /api/w/{workspace}/clients/{clientId}/input-config-audit`
- `GET /api/w/{workspace}/worker-tags`
- `POST /api/w/{workspace}/jobs/run/{app}/{action}`
- `POST /api/w/{workspace}/jobs/run/{app}/{action}/wait?timeout_ms={ms}`
- `POST /api/w/{workspace}/jobs/webhook/{app}/{action}`
- `GET /api/w/{workspace}/jobs?status={status}&limit={limit}`
- `GET /api/w/{workspace}/jobs/summary`
- `GET /api/w/{workspace}/jobs/{jobID}`
- `GET /api/w/{workspace}/jobs/{jobID}/result`
- `GET /api/w/{workspace}/jobs/{jobID}/logs?tail_bytes={bytes}`
- `POST /api/w/{workspace}/jobs/{jobID}/cancel`
- `GET|POST /api/w/{workspace}/state?path={path}` (canonical `ctx.state` helper storage)
- `GET|POST /api/w/{workspace}/variables`
- `GET /api/w/{workspace}/variables/get/p/{path}`
- `DELETE /api/w/{workspace}/variables/p/{path}`
- `POST /api/w/{workspace}/resources`
- `GET /api/w/{workspace}/resources/get/p/{path}`

Protocol adapters use the Execution API instead of the control-plane storage
model:

- `POST /execution/v1/workspaces/{workspace}/runs`
- `GET /execution/v1/workspaces/{workspace}/runs/{runId}`
- `GET /execution/v1/workspaces/{workspace}/runs/{runId}/result`
- `POST /execution/v1/workspaces/{workspace}/runs/{runId}/cancel`
- `GET /execution/v1/workspaces/{workspace}/apps/{app}`
- `GET /execution/v1/openapi.json`

Trusted protocol adapters may include `client_key` when creating a Run. The
value identifies a Client Registry record and selects client-scoped input
settings; it is not an API credential. Run admission rejects unknown client
keys and caller-supplied values for locked input keys.

The core script context exposes the implemented basic helpers:
`ctx.variables`, `ctx.resources`, `ctx.state`, `ctx.http`, `ctx.logger`,
and the run identity fields. Full Windforce flow approval URL minting
(`ctx.approval.getResumeUrls` / `POST /flow/resume-urls`) depends on the full
flow-run/step model and is intentionally not part of the core basic control
plane. Lite HITL uses the `WAITING_HUMAN` human-task resume API instead.

`git_sources` responses follow the canonical control-plane shape: `id` is the
numeric source identifier used by `{gitSourceId}` routes, and `name` is the
human-readable source name. Control-plane integrations, including the core CLI,
must store and call the returned numeric `id`.

`creds_ref` is a workspace-shared variable path for the git access token, not an
environment variable name. Register the token through the control-plane
variables API with an empty `app_key`, then pass that path as `creds_ref`. The
core CLI reads secret values from an environment variable so the token is not
placed in shell history:

```powershell
$env:WINDFORCE_LITE_GIT_TOKEN = "<token>"
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty variable-set `
  --path secrets/git/token --value-env WINDFORCE_LITE_GIT_TOKEN --secret
```

The Makefile wrapper uses the same API:

```powershell
$env:WINDFORCE_LITE_GIT_TOKEN = "<token>"
make windforce-git-token
```

The Makefile keeps the source name and route id separate for this reason:
`WF_GIT_SOURCE_NAME` is the human-readable name used by `make
windforce-register`; `WF_GIT_SOURCE_ID` is the numeric `id` returned by the
control plane and used by `make windforce-sync`. `WF_GIT_CREDS_REF` defaults to
`secrets/git/token`.

For local development without the Web UI, `tools/windforce_control.py` calls
the same control-plane API. The examples below target the Docker Compose and
Makefile default API URL, `http://127.0.0.1:18091`. Use a custom URL only when
running `go run ./cmd/windforce-core standalone --dev --addr <addr>` directly.

```powershell
python tools/windforce_control.py --api-url http://127.0.0.1:18091 register `
  --name echo --repo-url . --subpath examples/echo --creds-ref secrets/git/token
python tools/windforce_control.py --api-url http://127.0.0.1:18091 sync --git-source-id 1
python tools/windforce_control.py --api-url http://127.0.0.1:18091 sample --app-key sample_hello
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty run-wait `
  --app echo --action echo --input '{"message":"hello"}' --timeout-ms 5000
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty jobs --status completed
python tools/windforce_control.py --api-url http://127.0.0.1:18091 variables
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty schema `
  --app echo --action echo
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty control-openapi
```

Webhook subscriptions use the same authenticated control-plane API. Keep an
operator-supplied signing secret in an environment variable, or omit
`--secret-env` and retain the generated secret returned by `webhook-create`.
Stored secrets and endpoint paths cannot be read back.

```powershell
$env:WINDFORCE_WEBHOOK_SECRET = "replace-with-a-local-secret"
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty webhook-create `
  --name release-notifier --endpoint https://hooks.example.test/windforce `
  --secret-env WINDFORCE_WEBHOOK_SECRET --app-key echo
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty webhook-subscriptions
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty webhook-test `
  --webhook-id whs_example
python tools/windforce_control.py --api-url http://127.0.0.1:18091 --pretty webhook-deliveries `
  --webhook-id whs_example --state failed
```

The schema command reads the control-plane schema endpoint,
`GET /api/w/{workspace}/apps/{app}/actions/{action}/schema`, and prints the
materialized `input_schema` and `output_schema` JSON Schema documents. The
canonical action detail endpoint still keeps Windforce's catalog encoding:
`GET /api/w/{workspace}/apps/{app}/actions/{action}` returns base64-encoded
schema bytes.

Action schemas are exposed to operators through the control-plane API and to
protocol adapters through
`GET /execution/v1/workspaces/{workspace}/apps/{app}`. Adapters translate their
ingress and response envelopes while Windforce keeps release and schema
selection authoritative. The canonical app invocation schema endpoint is
`GET /api/w/{workspace}/apps/{app}/openapi.json`.
`windforce-core` additionally exposes `GET /api/w/{workspace}/openapi.json`
only as generated documentation for the supported core control-plane subset.
The workspace `control-openapi` command reads that documentation endpoint,
while the app `openapi` command returns invocation OpenAPI generated from the
decoded action schemas.
Lite deployment/source sync history is exposed through
`GET /api/w/{workspace}/apps/{app}/history`. The full Windforce draft
deployment status route, `GET /api/w/{workspace}/deployments/{deploymentID}`,
depends on the full deploy control-plane state table and is not part of the
core basic control plane.

The full Windforce control plane derives job actor provenance from the
authenticated principal. Lite keeps the same response fields without
implementing the full user/session principal model: local control-plane clients
may provide `X-Windforce-Actor` directly or use the core CLI's global `--actor`
option / `WINDFORCE_LITE_ACTOR` environment variable. `created_by`,
`permissioned_as`, and `canceled_by` fall back to `system` only when no actor is
present.

PostgreSQL is the production state backend. All runtime modes accept
`--state-backend postgres`, `--database-url`, and `--migrate`:

```powershell
$env:WINDFORCE_DATABASE_URL = "postgres://user:pass@host:5432/windforce_core?sslmode=disable"

go run ./cmd/windforce-core control-plane `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL `
  --migrate

go run ./cmd/windforce-core worker `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL

go run ./cmd/windforce-core webhook-dispatcher `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL
```

API token checks are optional for local development. `--admin-token-env` gates
the control-plane API. Worker scripts receive `WF_TOKEN` as a short-lived
job token signed with `--job-token-secret-env`; when that flag is omitted,
the admin token value is reused as the local signing secret so the raw admin
token is not injected into scripts:

```powershell
go run ./cmd/windforce-core control-plane `
  --admin-token-env WINDFORCE_ADMIN_TOKEN `
  --job-token-secret-env WINDFORCE_JOB_TOKEN_SECRET
```

Job input and job result output are stored with the same Windforce
`{"__wf_enc":1,"ct":"..."}` envelope used by the canonical worker when
`SECRET_KEY` is configured. The core API and worker must use the same
`--secret-key-env` / `--secret-key-previous-env` values; when omitted, both use
the local development default so standalone and compose runs continue to work
without extra setup.

Input settings are resolved in this order: app default, action default, client
app, client action, then request input. Each layer performs a shallow top-level
merge. Locked keys are the union of all applied layers, and their configured
values cannot be overridden by the request. Setting values are encrypted at
rest and are merged by the worker after it decrypts the persisted job input, so
configured values are not copied into Run or Job records.

Release publication stores a CloudEvents event and matching Webhook deliveries
in the same state transaction as the active release. The separate
`webhook-dispatcher` process claims those deliveries and sends signed HTTP
requests after the release transaction commits. Delivery uses
`X-Windforce-Event` (event ID), `X-Windforce-Event-Type`,
`X-Windforce-Delivery`, `X-Windforce-Timestamp`, and `X-Windforce-Signature`
headers. The signature is
`v1=<hex HMAC-SHA256(secret, timestamp + "." + rawBody)>`.

The versioned JSON Schema, fixtures, receiver rules, and runnable local
receiver are published in
[`contracts/webhooks/v1`](contracts/webhooks/v1/README.md). Messenger-specific
connectors verify this generic contract and own provider credentials, message
templates, rate limits, and durable event-ID deduplication.

Webhook endpoints use HTTPS by default. DNS results are checked again for each
attempt, private addresses require an explicit host or CIDR allowlist, and
redirects are not followed. A host-run local receiver may use HTTP loopback only
when `WINDFORCE_LITE_WEBHOOK_ALLOW_INSECURE_LOOPBACK=true`. Endpoint paths,
queries, signing secrets, and response bodies are not written to delivery logs.

The dedicated dispatcher exposes Prometheus metrics on
`WINDFORCE_LITE_WEBHOOK_METRICS_ADDR` (default `:9090`); `standalone` exposes
the same metrics at `/metrics` on its existing HTTP listener. Metric labels are
limited to event type, delivery state, and attempt outcome. Useful alert rules
include a nonzero increase of
`windforce_webhook_deliveries_total{state="failed"}` and
`windforce_webhook_oldest_pending_seconds` exceeding the expected delivery
delay.

Webhook delivery records are pruned in bounded batches by the dispatcher.
Succeeded and canceled deliveries default to 30 days, failed deliveries to 90
days, and pending/retrying/delivering records are never pruned. Events are
removed only after their deliveries are gone; soft-deleted subscriptions are
removed only after their deliveries are gone. Configure the terminal TTLs with
`WINDFORCE_LITE_WEBHOOK_SUCCESS_RETENTION_DAYS` and
`WINDFORCE_LITE_WEBHOOK_FAILURE_RETENTION_DAYS`; `0` keeps that outcome
forever. `WINDFORCE_LITE_WEBHOOK_RETENTION_INTERVAL`,
`WINDFORCE_LITE_WEBHOOK_RETENTION_BATCH_SIZE`, and
`WINDFORCE_LITE_WEBHOOK_RETENTION_TIME_BUDGET` bound cleanup work.

## Runtime architecture

Windforce Core has three explicit planes:

- Control Plane manages sources, releases, configuration, and audit history.
- Trigger Plane contains protocol adapters that call the Execution API.
- Execution Plane admits Runs, owns the PostgreSQL queue, and runs Jobs.

Run admission resolves and pins the active release before a Job is enqueued.
Workers poll the queue, fetch the pinned execution bundle, validate its
preparation fingerprint, and execute it. HITL pauses a Run in `WAITING_HUMAN`;
a resume operation enqueues its next Job.

Release publication prepares source by workspace/git-source/commit, installs
dependencies, injects the matching SDK, and compiles when required. A `.ready`
marker records the preparation fingerprint before the complete tree is
published to the Execution Artifact Store under its SHA-256 digest.

Each worker keeps a disposable cache by execution bundle digest. The
`.windforce-execution-ready` marker is written only after that worker has
fetched the artifact and accepted its preparation fingerprint. Workers do not
install dependencies, compile source, or contact Git while processing a Job.
See [Core concepts](docs/concepts/core-concepts.md) for the distinction between
the two markers.

Process roles are separated:

- `windforce-core control-plane`: source, release, configuration, audit, and Web UI APIs
- `windforce-core execution-api`: run admission and job-scoped runtime callbacks
- `windforce-core worker`: job polling and action execution
- `windforce-core webhook-dispatcher`: signed Control Plane event delivery and retry
- `windforce-core standalone`: local/dev combined mode

Protocol adapters adapt routes, request terms, environment variables, and
response envelopes at the edge. They call the Execution API through an SDK and
do not own source sync, queue records, or the Windforce catalog model. See
[Architecture](docs/architecture.md) for the dependency rules.

## Lightweight Admin UI

The Web UI is intentionally narrow:

- register apps backed by git repository sources
- publish releases (validate a source at HEAD and expose the worker contract)
- show release history and the currently active contract per app
- monitor aggregate job activity per app and route tag (queued, running,
  recent completed/failed/canceled, failure rate)
- review released action schemas (the materialized invocation contract)
- link to the app source on GitHub/GitLab at the pinned release commit
  instead of mirroring code in the UI
  ([ADR 0006](docs/adr/0006-source-links-not-source-mirror.md))

The UI deliberately shows aggregates, not individual job records: at
production volume nobody reads millions of rows. Per-run payloads, logs, and
cancel stay on the control-plane API and `tools/windforce_control.py`
([ADR 0005](docs/adr/0005-aggregate-job-observability.md)).

It is not the full Windforce console: no SaaS tenant management, billing, quota,
scheduler UI, workflow designer, or marketplace. The screen model is documented
in [docs/web-ui-model.md](docs/web-ui-model.md) and the generated user guide in
[docs/user-guide/web-ui.md](docs/user-guide/web-ui.md).

Raw job records are retained per outcome and pruned by the API process:
succeeded runs for 7 days, failed/canceled runs for 30 days, and queued or
running runs that make no progress for 24 hours are expired into the failure
family first. Tune with `--job-success-retention`, `--job-failure-retention`,
and `--job-stuck-after` (or `WINDFORCE_LITE_JOB_SUCCESS_RETENTION_DAYS`,
`WINDFORCE_LITE_JOB_FAILURE_RETENTION_DAYS`,
`WINDFORCE_LITE_JOB_STUCK_AFTER_HOURS`); `0` disables a rule. Release history
and the audit trail live in the catalog and are not affected. See
[ADR 0007](docs/adr/0007-job-storage-retention.md).

The local backend stores run, job, event, and HITL state in a JSON file for
development and smoke checks. The PostgreSQL backend stores production run, job,
event, and HITL state. Redis is optional for notification/cache only. See
[ADR 0002](docs/adr/0002-postgres-runtime-and-hitl.md).

## License

windforce-core is licensed under the [Apache License, Version 2.0](LICENSE).
