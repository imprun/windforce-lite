# Architecture

Windforce Core separates deployment management, protocol ingress, and action
execution into three planes. Compose runs the Control Plane, Execution API,
workers, and Webhook Dispatcher as distinct processes. The `standalone` command
combines them for single-process development without changing their contracts.

```text
operators / CI / Web UI
          |
          v
    Control Plane ------> source bundles + active release catalog
          |                       |
          v                       |
 event + delivery outbox          |
          |                       |
          v                       |
 Webhook Dispatcher ---> signed HTTPS endpoint
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

A workspace is an organizational scoping partition inside one engine instance
— it is **not** a tenant isolation boundary. Authentication, the worker pool,
bundle storage, and the encryption root are instance-wide. Operators who need
isolation between mutually untrusting tenants should run one engine instance
per tenant.

Publishing a release materializes the source bundle and action schemas before
the catalog points at that release. Workers and protocol adapters never clone a
repository or read repository credentials.

The selected state backend owns the active release catalog. A publication writes
the active release, immutable release history, source release marker, and audit
record in one transaction. Local mode persists the catalog in its state JSON
file; PostgreSQL mode persists it in control-plane tables shared by the Control
Plane and Execution API.

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

The Webhook Dispatcher reads only encrypted subscriptions, immutable event
bodies, and delivery state. It claims work with a lease, signs the CloudEvents
body, sends it outside the release transaction, and records success, terminal
failure, or a scheduled retry. Multiple PostgreSQL-backed dispatchers may run
at once; row locks prevent duplicate active claims while expired leases remain
recoverable.

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

HTTP contracts, message queues, schedulers, and webhooks call the versioned
Execution API through an execution SDK. They do not write queue tables or read
catalog files.

## Execution Plane

The Execution Plane owns run admission, the PostgreSQL queue, runtime workers,
execution results, and job-scoped runtime callbacks. Its public HTTP contract is
rooted at `/execution/v1`; workers receive the Execution API as `WF_API_URL` for
state, variable, and resource callbacks.

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
and catalog storage are private implementation details of Windforce Core.
