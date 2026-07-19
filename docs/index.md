---
title: Windforce Lite
description: Source synchronization, release publication, and execution for script applications.
---

Windforce Lite is a small control plane and execution runtime for applications
described by `windforce.json`. It synchronizes an exact Git revision, publishes
a prepared execution bundle, pins that release when a Run is admitted, and
executes the pinned bundle on a worker.

The product is organized around two independent paths:

- The **release path** turns Git source into an immutable, worker-ready
  execution bundle and selects an active release.
- The **execution path** accepts an app and action, pins the active release into
  a Job, and runs that exact bundle without contacting Git.

## Start here

1. [Core concepts](concepts/core-concepts.md) defines apps, synchronized
   revisions, releases, stores, caches, fingerprints, Runs, and Jobs.
2. [Release and execution lifecycle](concepts/release-lifecycle.md) explains
   Register, Sync, Publish Release, and Run in order.
3. [Architecture](architecture.md) defines the Control, Trigger, and Execution
   Plane boundaries.
4. [Control Plane CLI](cli.md) covers installation, profiles, releases, jobs,
   and provisioning automation.

## Documentation hosting

The `docs` directory is the documentation root for both supported hosting
options. The Markdown content is shared; only the site configuration differs.

- **Mintlify:** connect the repository and set the documentation directory to
  `docs`. Mintlify reads `docs/docs.json`.
- **GitHub Pages:** configure the publishing source as the `main` branch and
  `/docs` directory. GitHub Pages reads `docs/index.md` and
  `docs/_config.yml`.

The product documentation describes the current implementation. Design history
and decision rationale remain in the `docs/adr` records and are not presented as
current operator instructions.
