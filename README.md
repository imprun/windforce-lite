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

## Run

A queued run executes an action from the active catalog:

1. Read the app deployment from the catalog.
2. Find the requested action.
3. Fetch the deployment source by the deployment's pinned
   workspace/git-source/commit into a local runtime cache.
4. Execute the action command from the fetched source directory.
5. Pass JSON input/output paths through environment variables.
6. Store stdout/stderr as job logs and return exit code, duration, and output
   JSON.

The direct `run` CLI is a local smoke-test path and still prints the observed
subprocess fields in its JSON result.

## Manifest

Every app source has a `windforce.json` file:

```json
{
  "app": "echo",
  "actions": {
    "echo": {
      "runtime": "go",
      "command": ["go", "run", "./action.go"],
      "adapter": { "type": "json-file" },
      "timeoutMs": 30000
    }
  }
}
```

`command` is executed from the fetched app source directory. If `adapter` is
omitted, windforce-lite uses the built-in `json-file` adapter.

## Action adapters

An action adapter defines the contract between windforce-lite and the action
script. Built-in adapter types:

- `json-file`: runs `command` directly with file-based JSON IO.
- `command`: runs an external adapter command. The external adapter receives a
  Windforce adapter request and decides how to call the real script.

Example external adapter:

```json
{
  "app": "legacy-app",
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

- `WINDFORCE_ADAPTER_REQUEST_JSON`: request JSON file path
- `WINDFORCE_ADAPTER_RESULT_JSON`: result JSON file path
- `WINDFORCE_APP`: app name
- `WINDFORCE_ACTION`: action name

The request JSON includes `version`, `workDir`, `command`, `inputPath`,
`outputPath`, `app`, `action`, `runtime`, `entrypoint`, `timeoutMs`, `env`,
`actionSpec`, `deployment`, and `options`. The adapter should write the action
output JSON to `outputPath`, then write a result JSON compatible with
`JobResult` subprocess fields: `exitCode`, `stdout`, `stderr`, and
`durationMs`. In worker/API mode, `stdout` and `stderr` are appended to the
job log stream rather than exposed as the run result.

## JSON file adapter contract

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
- `POST /v1/git-sources`
- `GET /v1/git-sources`
- `GET /v1/git-sources/{gitSourceId}`
- `POST /v1/sync`
- `GET /v1/catalog`
- `GET /v1/deployments/{app}`
- `GET /v1/apps/{app}/actions/{action}/schema`
- `GET /v1/runs/{runID}`
- `GET /api/w/{workspace}/git_sources`
- `POST /api/w/{workspace}/git_sources`
- `POST /api/w/{workspace}/git_sources/probe`
- `PATCH /api/w/{workspace}/git_sources/{gitSourceId}`
- `DELETE /api/w/{workspace}/git_sources/{gitSourceId}`
- `POST /api/w/{workspace}/git_sources/{gitSourceId}/sync`
- `GET /api/w/{workspace}/apps`
- `GET /api/w/{workspace}/apps?view=summary`
- `GET /api/w/{workspace}/apps/{app}`
- `GET /api/w/{workspace}/apps/{app}/source`
- `GET /api/w/{workspace}/apps/{app}/history`
- `GET /api/w/{workspace}/apps/{app}/actions/{action}` (`input_schema` and `output_schema` expose materialized schema JSON when the bundle store is configured)
- `GET /api/w/{workspace}/deployments/{app}`
- `POST /api/w/{workspace}/jobs/run/{app}/{action}`
- `POST /api/w/{workspace}/jobs/run/{app}/{action}/wait?timeout_ms={ms}`
- `GET /api/w/{workspace}/jobs?status={status}&limit={limit}`
- `GET /api/w/{workspace}/jobs/summary`
- `GET /api/w/{workspace}/jobs/{jobID}`
- `GET /api/w/{workspace}/jobs/{jobID}/result`
- `GET /api/w/{workspace}/jobs/{jobID}/logs?tail_bytes={bytes}`
- `POST /api/w/{workspace}/jobs/{jobID}/cancel`
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
