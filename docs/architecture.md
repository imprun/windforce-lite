---
title: Architecture
description: Control, Trigger, and Execution Plane ownership in Windforce Lite.
---

Windforce Core separates deployment management, protocol ingress, and action execution into planes with package and HTTP contracts. Those contracts are deployed through three process roles: `server`, `worker`, and `standalone`.

```text
operators / CI / clients / external adapters
                    |
                    v
       server: HTTP planes + Web UI
          |                 |
          |                 +-- Webhook Dispatcher ---> signed HTTPS endpoint
          v
 Source Store ---> active release catalog ---> Execution Artifact Store
                          |
                          +-- admission ---> PostgreSQL queue
                                                   |
                                                   v
                                             runtime workers
                                                   |
                                                   +-- local bundle cache
```

## Control Plane

The Control Plane owns repository registration, source validation, release
publication, active release selection, configuration, and audit history. Its
API is rooted at `/api/w/{workspace}`.

A workspace is a registered organizational and authorization partition inside
one engine instance. Workspace tokens cannot cross workspace paths. The worker
pool, bundle storage, database, and encryption root remain instance-wide, so
mutually untrusting tenants require separate engine instances. Workspace
lifecycle and access rules are defined in [Workspaces](concepts/workspaces.md).

Synchronization materializes the source revision and action schemas without
changing the active catalog. Publishing prepares the latest synchronized
revision as a worker-ready execution bundle before the catalog points at that
release. Workers and protocol adapters never clone a repository or read
repository credentials.

The Source Store contains exact source snapshots keyed by workspace, repository
source ID, and commit. The Execution Artifact Store contains complete prepared
trees keyed by SHA-256 digest. A worker keeps only a disposable local copy of a
pinned execution bundle. The current filesystem-backed deployment requires the
release builder and workers to access the same persistent artifact root. See
[Core concepts](concepts/core-concepts.md) for the store, cache, fingerprint,
and marker definitions, and
[Release and execution lifecycle](concepts/release-lifecycle.md) for the state
transitions.

The selected state backend owns the active release catalog. A publication writes
the active release, immutable release history, source release marker, and audit
record in one transaction. Local mode persists the catalog in its state JSON
file; PostgreSQL mode persists it in tables shared by the server and workers.

The same publication transaction stores a CloudEvents-compatible Control Plane
event and one pending delivery for each enabled matching Webhook subscription.
Endpoint and signing-secret values use workspace encryption. External HTTP
delivery is always outside the publication transaction.

The Control Plane API owns workspace-scoped Webhook subscription CRUD, test
events, delivery history, and failed-delivery retry. Read responses expose only
the endpoint scheme and host. A signing secret is returned only by the create
or rotation response, and every management change is included in the canonical
workspace audit stream. Deleted subscriptions remain visible only through the
explicit history query while pending deliveries are canceled.

## Webhook Dispatcher

The server runs a Webhook Dispatcher alongside its HTTP listener. The dispatcher reads only encrypted subscriptions, immutable event bodies, and delivery state. It claims work with a lease, signs the CloudEvents body, sends it outside the release transaction, and records success, terminal failure, or a scheduled retry. Every server replica may run the loop; PostgreSQL row locks prevent duplicate active claims while expired leases remain recoverable.

Each attempt resolves DNS again and connects directly to an address that passed
the egress policy. HTTPS is required except for explicitly enabled local HTTP
loopback. Redirects, link-local and metadata addresses are rejected. Private
addresses require a configured host or CIDR allowlist. Logs identify the
delivery and event type without endpoint paths, queries, signing secrets, or
response bodies.

## Trigger Plane

The Trigger Plane is a set of protocol adapters. A protocol adapter owns only
its inbound protocol and compatibility policy:

- route and message parsing
- caller authentication and request budgets
- mapping protocol fields to `app`, `action`, and `input`
- correlation and idempotency metadata
- mapping the generic run result to a protocol response

In-tree adapters running in the server call `execution.Service.CreateRun` in-process. External adapters and other languages call the versioned Execution API through an execution SDK. Both transports preserve the same `CreateRunRequest` semantics. Adapters do not write queue tables or read catalog files.

## Execution Plane

The Execution Plane owns run admission, the PostgreSQL queue, runtime workers,
execution results, and job-scoped runtime callbacks. Its public HTTP contract is
rooted at `/execution/v1`; workers receive the Execution API as `WF_API_URL` for
state, variable, and resource callbacks.

Run admission performs one atomic decision:

1. Resolve the active app release in the requested workspace.
2. Validate the action and worker capability routing.
3. Resolve InputConfig once, enforce LockedKeys, and validate the merged input against the active action schema.
4. Materialize the action input and output schemas.
5. Pin the effective input, deployment, commit, entrypoint, runtime, schemas, route, and timeout.
6. Create the caller-visible Run and its first internal Job in one transaction.

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

## Public API Plane

The Public API Plane is rooted at `/api/v1/w/{workspace}` and accepts only engine-issued `wfk_` client bearer tokens. It maps an authenticated client to client-scoped input settings, applies admission through the Execution Plane, and never writes the queue or catalog directly. Its async and wait routes return the admitted Job identifier in `X-WF-Job-Id`; an idempotent replay preserves that Job identifier, and the wait route returns its action result as the response body. See [Public API](concepts/public-api.md).

## SDK Boundary

The Python package under `sdk/python` is the reference execution client. It
provides create, status, wait, result, cancel, and app-description operations.
SDK implementations are HTTP clients only. PostgreSQL schemas, bundle paths,
and catalog storage are private implementation details of Windforce Core.

## Process Roles

| Role | Responsibility |
|---|---|
| `server` | Control `/api/w`, trusted execution `/execution/v1`, public `/api/v1`, worker `/worker/v1`, embedded Web UI, Webhook Dispatcher, and retention loops |
| `worker` | Queue claim and action execution, using shared PostgreSQL state or the remote worker API selected by `--api-url` |
| `standalone` | `server` and `worker` in one process |

The HTTP plane boundaries separate caller trust and API contracts, not processes. Server replicas expose every HTTP plane and may safely run the dispatcher concurrently. Internal package boundaries remain independent so an adapter can move between in-process and HTTP deployment without changing admission semantics.
