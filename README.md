# windforce-lite

`windforce-lite` is a small source-sync runtime for Windforce-style apps.

It keeps the useful core of Windforce:

- register or point at a source tree
- materialize a source-only bundle by workspace, git source id, and commit
- publish an active app/action deployment in a catalog
- track deployment history
- fetch the bundle before execution
- run the app entrypoint with `main(ctx)` and dispatch by action

It intentionally does not include the full Windforce product surface:
multi-tenant SaaS concerns, quota, scheduler, billing, or an operator. A small
admin Web UI for git source registration, deployment, and deployment history is
in scope.

## Concepts

- App: the deployable source bundle
- Action: one executable unit inside an app
- Deployment: the selected app commit/digest and its action metadata
- Catalog: the active deployment index
- Bundle store: source-only object cache keyed by workspace/git-source/commit
- Deployment history: an audit trail of source syncs and deployment changes

## Sync

`sync` turns a registered or directly supplied source tree into an active
deployment:

1. Register a git source through the control-plane API, or pass a source
   explicitly for local smoke tests.
2. Resolve the source version.
   - local source: compute a source tree digest
   - git source: resolve the branch or commit
3. If the git source has a `subpath`, use that repo directory as the app root
   and try sparse checkout before falling back to a full clone.
4. Load `windforce.json`.
5. Materialize the source tree into the bundle store under
   `{workspace}/{gitSourceId}/{commit}`.
6. Write the catalog entry after the bundle is complete.

The ordering is intentional: a catalog entry must not point at a bundle that a
worker cannot fetch.

The Docker Compose control-plane runs inside a container, so the default
`make windforce-register` path registers a remote git URL. Use the direct
`sync` CLI for host-local source smoke tests.

## Run

A queued run executes an action from the active catalog:

1. Read the app deployment from the catalog.
2. Find the requested action.
3. Fetch the deployment source by the deployment's pinned
   workspace/git-source/commit into a local runtime cache.
4. Execute the app-level entrypoint from the fetched source directory.
5. Build the Windforce `ctx` object from `input.json` and `WF_*` environment.
6. Store stdout/stderr as job logs and return exit code, duration, and output
   JSON.

The direct `run` CLI is a local smoke-test path and still prints the observed
subprocess fields in its JSON result.

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

## Runtime adapter compatibility

The canonical runtime path runs `entrypoint -> main(ctx) -> result.json`.
`windforce-lite` still keeps a lower-level adapter subprocess API for integration
code that must adapt an existing script contract into the Windforce ctx contract.
That compatibility API is runtime/catalog integration code, not a field in
`windforce.json`.

An external adapter receives a Windforce adapter request and decides how to call
the real script. The shape below is deployment/catalog metadata owned by the
runtime integration layer; it is not valid `windforce.json` source manifest
content:

```json
{
  "app": "legacy-app",
  "entrypoint": "main.py",
  "scriptLang": "python",
  "actions": {
    "run": {
      "command": ["legacy-runtime", "run"],
      "adapter": {
        "type": "command",
        "command": ["legacy-windforce-adapter"],
        "options": {
          "mode": "compat"
        }
      }
    }
  }
}
```

The `command` adapter process receives:

- `WF_ADAPTER_REQUEST_JSON`: request JSON file path
- `WF_ADAPTER_RESULT_JSON`: result JSON file path
- `WF_APP`: app name
- `WF_ACTION`: action name

The request JSON includes `version`, `workDir`, `command`, `inputPath`,
`outputPath`, `app`, `action`, `runtime`, `entrypoint`, `timeoutMs`, `env`,
`actionSpec`, `deployment`, and `options`. The adapter should write the action
output JSON to `outputPath`, then write a result JSON compatible with
`JobResult` subprocess fields: `exitCode`, `stdout`, `stderr`, and
`durationMs`. In worker/API mode, `stdout` and `stderr` are appended to the
job log stream rather than exposed as the run result.

## Entrypoint contract

The executor writes `input.json` in a per-job directory, builds `ctx` from
`input.json` plus `WF_*`, imports the app entrypoint, calls `main(ctx)`, and
expects `result.json`. A non-zero process exit is returned as an action result,
not as a runner infrastructure error.

## Try it locally

```powershell
go run ./cmd/windforce-lite sync `
  --source examples/echo `
  --store .tmp/store `
  --catalog .tmp/catalog.json

'{"message":"hello"}' | Set-Content -Encoding utf8 .tmp/input.json

go run ./cmd/windforce-lite run `
  --app echo `
  --action echo `
  --input .tmp/input.json `
  --output .tmp/output.json `
  --store .tmp/store `
  --catalog .tmp/catalog.json
```

The same flow works with a git source:

```powershell
go run ./cmd/windforce-lite sync `
  --repo https://github.com/imprun/example-windforce-app.git `
  --branch main `
  --subpath apps/echo `
  --store .tmp/store `
  --catalog .tmp/catalog.json
```

## Local runtime mode

The direct `run` command is useful for smoke tests. The runtime process model is
available through local file-backed state:

```powershell
go run ./cmd/windforce-lite standalone `
  --addr :8080 `
  --store .tmp/store `
  --catalog .tmp/catalog.json `
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

Separated local processes use the same state file:

```powershell
go run ./cmd/windforce-lite api --addr :8081 --state .tmp/state.json
go run ./cmd/windforce-lite worker --state .tmp/state.json --store .tmp/store
```

`worker --once` claims at most one queued job and exits, which is useful in
tests and scripted smoke checks.
`worker --tags default,app-blue` restricts claims to those pinned route tags;
when omitted, the worker claims every queued tag for simple local development.

Implemented control-plane endpoints:

- `GET /api/w/{workspace}/openapi.json` (workspace control-plane OpenAPI)
- `GET /api/w/{workspace}/git_sources`
- `POST /api/w/{workspace}/git_sources`
- `POST /api/w/{workspace}/git_sources/probe`
- `POST /api/w/{workspace}/git_sources/sample`
- `PATCH /api/w/{workspace}/git_sources/{gitSourceId}`
- `DELETE /api/w/{workspace}/git_sources/{gitSourceId}`
- `POST /api/w/{workspace}/git_sources/{gitSourceId}/sync`
- `GET /api/w/{workspace}/apps`
- `GET /api/w/{workspace}/apps?view=summary`
- `GET /api/w/{workspace}/apps/{app}`
- `PATCH /api/w/{workspace}/apps/{app}`
- `POST /api/w/{workspace}/apps/{app}/requeue`
- `GET /api/w/{workspace}/apps/{app}/source`
- `GET /api/w/{workspace}/apps/{app}/history`
- `GET /api/w/{workspace}/apps/{app}/openapi.json` (app invocation OpenAPI generated from materialized action schemas)
- `GET /api/w/{workspace}/apps/{app}/actions/{action}` (canonical action detail including materialized `input_schema` and `output_schema`)
- `PATCH /api/w/{workspace}/apps/{app}/actions/{action}`
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

`git_sources` responses follow the canonical control-plane shape: `id` is the
numeric source identifier used by `{gitSourceId}` routes, and `name` is the
human-readable source name. Control-plane integrations, including the lite CLI,
must store and call the returned numeric `id`.

`creds_ref` is a workspace-shared variable path for the git access token, not an
environment variable name. Register the token through `POST
/api/w/{workspace}/variables` with an empty `app_key`, then pass that path as
`creds_ref`.

The Makefile keeps the source name and route id separate for this reason:
`WF_GIT_SOURCE_NAME` is the human-readable name used by `make
windforce-register`; `WF_GIT_SOURCE_ID` is the numeric `id` returned by the
control plane and used by `make windforce-sync`.

For local development without the full UI, `tools/windforce_control.py` calls
the same control-plane API:

```powershell
python tools/windforce_control.py --api-url http://127.0.0.1:8080 register `
  --name echo --repo-url . --subpath examples/echo
python tools/windforce_control.py --api-url http://127.0.0.1:8080 sync --git-source-id 1
python tools/windforce_control.py --api-url http://127.0.0.1:8080 sample --app-key sample_hello
python tools/windforce_control.py --api-url http://127.0.0.1:8080 --pretty schema `
  --app echo --action echo
python tools/windforce_control.py --api-url http://127.0.0.1:8080 --pretty control-openapi
```

The schema command reads the canonical action detail endpoint,
`GET /api/w/{workspace}/apps/{app}/actions/{action}`, then prints the
materialized `input_schema` and `output_schema`.

Action schemas are exposed through the Windforce control-plane API. Protocol
adapters may translate trigger ingress and response envelopes, but they do not
publish separate schema routes or own schema discovery. The workspace
`control-openapi` command documents that control-plane contract, while the app
`openapi` command returns invocation OpenAPI generated from the action schemas.
Lite deployment/source sync history is exposed through
`GET /api/w/{workspace}/apps/{app}/history`. The full Windforce draft
deployment status route, `GET /api/w/{workspace}/deployments/{deploymentID}`,
depends on the full deploy control-plane state table and is not part of the
lite basic control plane.

The full Windforce control plane derives job actor provenance from the
authenticated principal. Lite deployments that use only the admin token can pass
`X-Windforce-Actor` or `X-Windforce-User` on cancel requests so `canceled_by`
matches the operator identity; without either header it falls back to the job's
recorded actor.

PostgreSQL is the production state backend. All runtime modes accept
`--state-backend postgres`, `--database-url`, and `--migrate`:

```powershell
$env:WINDFORCE_DATABASE_URL = "postgres://user:pass@host:5432/windforce_lite?sslmode=disable"

go run ./cmd/windforce-lite api `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL `
  --migrate

go run ./cmd/windforce-lite worker `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL
```

API token checks are optional for local development. Set token values through
environment variables and pass only the variable names to the process:

```powershell
go run ./cmd/windforce-lite api `
  --admin-token-env WINDFORCE_ADMIN_TOKEN
```

## Runtime architecture

The runtime follows the original Windforce control-plane/worker model:

- the control-plane run API creates a run and enqueues a job
- worker polls the queue and executes the pinned deployment
- HITL pauses a run in `WAITING_HUMAN`
- resume API enqueues the next job

Production process roles are separated:

- `windforce-lite api`: control plane, HTTP run ingress, run status, HITL resume
- `windforce-lite worker`: job polling and action execution
- `windforce-lite standalone`: local/dev combined mode

Protocol adapters should live outside this core repository unless they are
generic Windforce adapters. They adapt routes, request terms, environment
variables, and response envelopes at the edge; they do not own source sync or
mutate the Windforce catalog model.

## Lightweight Admin UI

The Web UI is intentionally narrow:

- register git sources
- sync a source and deploy an app/action catalog entry
- show deployment history and currently active deployment
- inspect run status and errors
- roll back to a previous deployment when the source object is still available

It is not the full Windforce console: no SaaS tenant management, billing, quota,
scheduler UI, workflow designer, or marketplace.

The local backend stores run, job, event, and HITL state in a JSON file for
development and smoke checks. The PostgreSQL backend stores production run, job,
event, and HITL state. Redis is optional for notification/cache only. See
[ADR 0002](docs/adr/0002-postgres-runtime-and-hitl.md).
