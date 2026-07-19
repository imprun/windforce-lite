---
title: Control Plane CLI
description: Install and use the supported Windforce command-line client.
---

The `windforce` command is the supported non-interactive client for the
Windforce Core Control Plane API. It uses the same API for a loopback stack and
a hosted workspace. Changing the selected profile changes the target without
changing source, release, or job commands.

## Install

Tagged releases contain archives for Windows, macOS, and Linux on amd64 and
arm64. Download the archive for the host from
[GitHub Releases](https://github.com/imprun/windforce-core/releases).

On Windows, extract the ZIP file and place `windforce.exe` in a directory on
`PATH`:

```powershell
Expand-Archive .\windforce_<VERSION>_windows_amd64.zip -DestinationPath .\windforce
windforce\windforce.exe version
```

On macOS or Linux, extract the matching archive and install the executable in a
directory on `PATH`:

```shell
tar -xzf windforce_<VERSION>_<OS>_<ARCH>.tar.gz
install -m 0755 windforce "$HOME/.local/bin/windforce"
windforce version
```

Upgrade by replacing the executable with one from a newer tagged release. User
profiles stay in the operating system's user configuration directory and do
not need to move with the executable.

The repository build produces the same executable:

```shell
make cli-build
```

The output is `.tmp/bin/windforce` (`.exe` on Windows when built by Go for
Windows).

## Configure a profile

Profiles store connection metadata. A profile stores the name of the
environment variable containing a bearer token; it never stores the token
value.

```powershell
$env:WINDFORCE_API_TOKEN = "<TOKEN>"
windforce profile set local `
  --api-url http://127.0.0.1:18091 `
  --workspace default `
  --actor developer@example.test `
  --token-env WINDFORCE_API_TOKEN `
  --use
```

Use `windforce profile list`, `windforce profile show`, and `windforce profile
use <name>` to inspect or select profiles. Set `WINDFORCE_CONFIG` to use an
explicit profile file. Global flags override profile values:

```shell
windforce --profile staging --pretty app list --summary
windforce --api-url https://windforce.example.test --workspace team app list
```

## Release path

Register reads Git credentials only from an environment variable. The CLI
stores the resulting credential as a secret Control Plane variable and sends
its reference when registering the source.

```powershell
$env:GIT_ACCESS_TOKEN = "<TOKEN>"
windforce source probe `
  --repo-url https://git.example.test/team/app.git `
  --branch main `
  --auth-method pat `
  --access-token-env GIT_ACCESS_TOKEN

windforce source register `
  --name example `
  --repo-url https://git.example.test/team/app.git `
  --branch main `
  --subpath apps/example `
  --auth-method pat `
  --access-token-env GIT_ACCESS_TOKEN

windforce source list
windforce source sync 12
windforce source deploy 12 --message "Publish validated revision"
```

The numeric source ID returned by register or list is used by sync and deploy.
Workers do not receive Git credentials and do not contact the repository.

## Inspect apps and API schemas

```shell
windforce app list --summary
windforce app show example
windforce app history example
windforce action show example health
windforce action schema example health
windforce app openapi example
windforce openapi
```

Commands emit compact JSON by default. Add the global `--pretty` flag before
the command for human-readable JSON.

## Run and inspect jobs

```shell
windforce job run example health --input '{"ping":true}'
windforce job run example parse --input-file input.json --wait --timeout-ms 30000
windforce job list --app example --status running
windforce job show <JOB_ID>
windforce job result <JOB_ID>
windforce job logs <JOB_ID> --tail-bytes 65536
windforce job cancel <JOB_ID> --reason "operator request"
```

`--input-file -` reads JSON from standard input. Job logs are written as the
raw response so they can be piped to another command.

## Provisioning

```shell
windforce provisioning export --format yaml --output windforce.yaml
windforce provisioning apply --file windforce.yaml --dry-run
windforce provisioning apply --file windforce.yaml
```

Exported secret values remain redacted. Environment-specific secret resources
must use the provisioning `valueFrom` contract.

## Exit codes

| Code | Meaning |
| ---: | --- |
| `0` | Command completed successfully |
| `2` | Invalid command or arguments |
| `3` | Invalid local profile or configuration |
| `10` | Local I/O or HTTP transport failure |
| `20` | Control Plane returned a 4xx response |
| `21` | Control Plane returned a 5xx response |

JSON API errors are written to standard error. This makes the command suitable
for CI without parsing human-formatted messages.

## Repository helper

`tools/windforce_control.py` is a repository-local API helper. The released
`windforce` executable is the installed client contract for source, app,
action, job, OpenAPI, and provisioning operations. Repository-only maintenance
commands can continue to use the helper without making Python a requirement
for product repositories.
