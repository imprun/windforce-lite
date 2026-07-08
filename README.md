# windforce-lite

`windforce-lite` is a small source-sync runtime for Windforce-style apps.

It keeps the useful core of Windforce:

- register or point at a source tree
- materialize a source-only bundle by workspace, git source id, and commit
- publish an active app/action deployment in a catalog
- track deployment history
- fetch the bundle before execution
- run an action as a JSON subprocess

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

`sync` turns a source tree into an active deployment:

1. Resolve the source version.
   - local source: compute a source tree digest
   - git source: resolve the branch or commit
2. Load `windforce.json`.
3. Materialize the source tree into the bundle store under
   `{workspace}/{gitSourceId}/{commit}`.
4. Write the catalog entry after the bundle is complete.

The ordering is intentional: a catalog entry must not point at a bundle that a
worker cannot fetch.

## Run

`run` executes an action from the active catalog:

1. Read the app deployment from the catalog.
2. Find the requested action.
3. Fetch the deployment source by the deployment's pinned
   workspace/git-source/commit into a local runtime cache.
4. Execute the action command from the fetched source directory.
5. Pass JSON input/output paths through environment variables.
6. Return stdout, stderr, exit code, duration, and output JSON.

## Manifest

Every app source has a `windforce.json` file:

```json
{
  "app": "echo",
  "actions": {
    "echo": {
      "runtime": "go",
      "command": ["go", "run", "./action.go"],
      "timeoutMs": 30000
    }
  }
}
```

`command` is executed from the fetched app source directory.

## JSON subprocess contract

The runner passes file paths through environment variables:

- `WINDFORCE_INPUT_JSON`: input JSON file path
- `WINDFORCE_OUTPUT_JSON`: output JSON file path
- `WINDFORCE_APP`: app name
- `WINDFORCE_ACTION`: action name

An action reads `WINDFORCE_INPUT_JSON` and writes a JSON value to
`WINDFORCE_OUTPUT_JSON`. A non-zero process exit is returned as an action result,
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
  --state .tmp/state.json `
  --wait 30s
```

Trigger an action through HTTP:

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/apps/echo/actions/echo `
  -ContentType application/json `
  -Body '{"message":"hello"}'
```

Separated local processes use the same state file:

```powershell
go run ./cmd/windforce-lite trigger --addr :8081 --state .tmp/state.json --wait 30s
go run ./cmd/windforce-lite api --addr :8082 --state .tmp/state.json
go run ./cmd/windforce-lite worker --state .tmp/state.json --store .tmp/store
```

`worker --once` claims at most one queued job and exits, which is useful in
tests and scripted smoke checks.

Implemented control-plane endpoints:

- `POST /v1/apps/{app}/actions/{action}`
- `POST /v1/sync`
- `GET /v1/catalog`
- `GET /v1/deployments/{app}`
- `GET /v1/apps/{app}/actions/{action}/schema`
- `GET /v1/runs/{runID}`
- `POST /v1/runs/{runID}/cancel`
- `POST /v1/runs/{runID}/retry`
- `GET /v1/human-tasks/{humanTaskID}`
- `POST /v1/human-tasks/{humanTaskID}/resume`
- `POST /v1/runs/{runID}/resume`

PostgreSQL is the production state backend. All runtime modes accept
`--state-backend postgres`, `--database-url`, and `--migrate`:

```powershell
$env:WINDFORCE_DATABASE_URL = "postgres://user:pass@host:5432/windforce_lite?sslmode=disable"

go run ./cmd/windforce-lite trigger `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL `
  --migrate

go run ./cmd/windforce-lite worker `
  --state-backend postgres `
  --database-url $env:WINDFORCE_DATABASE_URL
```

HTTP trigger/API token checks are optional for local development. Set token
values through environment variables and pass only the variable names to the
process:

```powershell
go run ./cmd/windforce-lite trigger `
  --trigger-token-env WINDFORCE_TRIGGER_TOKEN

go run ./cmd/windforce-lite api `
  --admin-token-env WINDFORCE_ADMIN_TOKEN
```

## Runtime architecture

The long-term runtime follows the original Windforce trigger/worker model:

- HTTP trigger creates a run and enqueues a job
- worker polls the queue and executes the pinned deployment
- HITL pauses a run in `WAITING_HUMAN`
- resume API enqueues the next job

Production process roles are separated:

- `windforce-lite trigger`: external execution ingress
- `windforce-lite api`: control plane, run status, HITL resume
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
