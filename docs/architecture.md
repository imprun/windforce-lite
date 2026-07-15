# Architecture

Windforce Lite separates deployment management, protocol ingress, and action
execution into three planes. The planes can run in one process for development,
but their contracts do not depend on that process layout.

```text
operators / CI / Web UI
          |
          v
    Control Plane ------> source bundles + active release catalog
                                  |
HTTP / queue / scheduler          |
protocol adapters                 v
          +------------> Execution API ----> PostgreSQL queue
                                  |                 |
                                  |                 v
                                  +-----------> runtime workers
```

## Control Plane

The Control Plane owns repository registration, source validation, release
publication, active release selection, configuration, and audit history. Its
API is rooted at `/api/w/{workspace}`.

Publishing a release materializes the source bundle and action schemas before
the catalog points at that release. Workers and protocol adapters never clone a
repository or read repository credentials.

## Trigger Plane

The Trigger Plane is a set of protocol adapters. A protocol adapter owns only
its inbound protocol and compatibility policy:

- route and message parsing
- caller authentication and request budgets
- mapping protocol fields to `app`, `action`, and `input`
- correlation and idempotency metadata
- mapping the generic run result to a protocol response

The built-in HTTP adapter is the versioned Execution API. Other HTTP contracts,
message queues, schedulers, and webhooks call the same API through an execution
SDK. They do not write queue tables or read catalog files.

## Execution Plane

The Execution Plane owns run admission, the PostgreSQL queue, runtime workers,
and execution results. Its HTTP contract is rooted at `/execution/v1`.

Run admission performs one atomic decision:

1. Resolve the active app release in the requested workspace.
2. Validate the action and worker capability routing.
3. Materialize the action input and output schemas.
4. Pin the deployment, commit, entrypoint, runtime, schemas, route, and timeout.
5. Create the caller-visible Run and its first internal Job in one transaction.

A Run is the stable caller-visible invocation. A Job is an internal execution
attempt. Workers execute only the deployment pinned in the Job payload; they do
not resolve the active catalog again.

## Execution API

- `POST /execution/v1/workspaces/{workspace}/runs`
- `GET /execution/v1/workspaces/{workspace}/runs/{run_id}`
- `GET /execution/v1/workspaces/{workspace}/runs/{run_id}/result`
- `POST /execution/v1/workspaces/{workspace}/runs/{run_id}/cancel`
- `GET /execution/v1/workspaces/{workspace}/apps/{app}`
- `GET /execution/v1/openapi.json`

`Idempotency-Key` or `idempotency_key` scopes duplicate suppression to a
workspace, app, and action. Replaying the same key returns the existing Run.

The app description endpoint returns the active release and materialized action
schemas. Protocol adapters use it to generate their own customer-facing API
documentation without mounting the Windforce catalog.

## SDK Boundary

The Python package under `sdk/python` is the reference execution client. It
provides create, status, wait, result, cancel, and app-description operations.
SDK implementations are HTTP clients only. PostgreSQL schemas, bundle paths,
and catalog storage are private implementation details of Windforce Lite.
