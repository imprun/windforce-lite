---
title: Public API
description: Invoke actions with workspace-scoped client credentials.
---

The Public API lets an external caller invoke an action with a Client Registry credential. A client token grants access only to public action routes in the client's workspace. It cannot call the control plane, the trusted Execution API, or another workspace.

## Client tokens

Creating a client returns a `wfk_` bearer once:

```http
POST /api/w/{workspace}/clients
Authorization: Bearer <WORKSPACE_OR_ADMIN_TOKEN>
Content-Type: application/json

{"name":"Example Client"}
```

The response contains `client` and `api_token`. The raw token is stored by the caller; Windforce Core stores only its SHA-256 hash. A Client Registry entry without an active token cannot call the Public API; issue or rotate one with `POST /api/w/{workspace}/clients/{client_id}/token`. Revoke it with `DELETE /api/w/{workspace}/clients/{client_id}/token`. Rotation and revocation invalidate the active token immediately. Revoke the active token before deleting a client.

## Invoke an action

Asynchronous invocation creates a Run and Job and returns immediately:

```http
POST /api/v1/w/{workspace}/run/{app}/{action}
Authorization: Bearer <CLIENT_TOKEN>
Content-Type: application/json

{"message":"hello"}
```

The response is `201 {"job_id":"..."}`. `X-WF-Job-Id` contains the same Job identifier.

Send `Idempotency-Key` when a caller may retry an invocation. The key is scoped to the workspace, client, app, and action. Replaying it returns the original Job identifier and result instead of admitting a second Job, including when the active release or input settings have changed since the first admission.

Wait invocation keeps the HTTP request open and returns the action result body:

```http
POST /api/v1/w/{workspace}/run/{app}/{action}/wait?timeout=30s
Authorization: Bearer <CLIENT_TOKEN>
Content-Type: application/json

{"message":"hello"}
```

A completed execution returns HTTP 200 with the raw JSON result. Execution failure is also an execution result and therefore uses HTTP 200. If the wait timeout expires first, the API returns HTTP 202 with `job_id` and `status: pending`. `X-WF-Job-Id` is present after admission for every response variant.

## Input settings

Input settings are applied from least specific to most specific:

1. app defaults for all clients;
2. action settings for all clients;
3. app settings for the authenticated client;
4. action settings for the authenticated client;
5. caller request fields that are not locked.

Later layers override earlier layers. A caller field named in any effective `locked_keys` list is rejected with HTTP 400; the stored operator value is not silently replaced.

Admission validates the merged input against the active release's action input schema from `windforce.json` and its companion JSON Schema files. From the caller's perspective, locked properties are supplied by InputConfig and are therefore absent from the writable request contract. The merged, validated input and release schemas are pinned in the Run and Job, so a queued execution is not changed by later catalog or InputConfig updates.

## HTTP status policy

| Status | Meaning |
| --- | --- |
| 200 | Wait invocation completed; the body is the action result |
| 201 | Asynchronous invocation admitted |
| 202 | Wait timeout expired while the Job is still pending |
| 400 | Invalid JSON, invalid action input, or attempted locked-key override |
| 401 | Missing, invalid, rotated, or revoked client token |
| 404 | App or action does not exist |
| 409 | Workspace is archived or admission conflicts |
| 429 | Instance Public API rate limit exceeded |
| 503 | Execution admission is unavailable |

Authentication failures are recorded in the workspace Client Audit stream without storing the presented token. Authenticated admission and rejection records include the client, app, action, Job identifier when available, and replay state, but never the request input or credential. The Public API applies an instance-wide token-bucket limiter before authentication. Configure it with `--public-api-rps` and `--public-api-burst`. The `server` and `standalone` roles expose this plane; `standalone` requires no additional enablement.
