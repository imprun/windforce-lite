---
title: Workspaces
description: Managed state and authorization boundaries inside one Windforce Lite instance.
---

A workspace groups apps, releases, clients, input settings, jobs, webhooks, variables, resources, and audit records inside one Windforce Lite instance. Every workspace has an immutable ID, a display name, a lifecycle status, and optional scoped API access.

## Identity

Workspace IDs are lowercase slugs. They contain 2 to 48 lowercase letters, digits, or hyphens, start with a letter, and end with a letter or digit. The ID is immutable because it is part of API paths and stored resource keys. The display name can be changed.

`default` is always registered and cannot be archived. It is the initial workspace for local development and installations that use one workspace.

## Access

Windforce Lite recognizes three API principals:

| Principal | Scope | Credential |
| --- | --- | --- |
| Instance administrator | Global workspace lifecycle and every workspace | Token configured by `--admin-token-env` |
| Workspace principal | One workspace only | Token returned once at workspace creation or rotation |
| Job principal | SDK callback endpoints for one job and workspace | Short-lived job token |

Workspace tokens are stored as SHA-256 hashes. The raw token is returned only when the workspace is created or its token is rotated. Rotation invalidates the current token immediately. Workspace principals cannot list, create, archive, or rotate workspaces and cannot access another workspace path.

When no instance-admin token is configured, local development accepts requests without authentication. Configure an instance-admin token for any shared environment.

## Lifecycle

An active workspace accepts control-plane changes and new execution requests. Archiving a workspace preserves its state and audit records while blocking configuration changes, releases, webhook changes, and new Runs. Read operations, audit queries, and provisioning export remain available. Job-scoped SDK callbacks remain available so running jobs can settle.

Workspace deletion and reactivation are not exposed. Use a separate workspace when a new active namespace is required.

## Operations

Use the sidebar workspace switcher to change the current workspace. Open **Manage workspaces** from the same switcher to use the instance administration page with an instance-admin token:

- create a workspace;
- change its display name;
- rotate its workspace token;
- inspect lifecycle audit records;
- archive an active workspace.

The administration page is instance-scoped and is separate from Settings, which applies to the currently selected workspace. Each workspace has dedicated Overview, Access, Audit, and Lifecycle tabs.

The global lifecycle API is rooted at `/api/workspaces`. Workspace resources remain rooted at `/api/w/{workspace}` and execution requests at `/execution/v1/workspaces/{workspace}`.
