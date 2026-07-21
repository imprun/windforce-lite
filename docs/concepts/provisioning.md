# Provisioning

Provisioning lets operators declare repeatable control-plane state as JSON or
YAML resources. It is useful for bootstrapping a workspace, moving app
configuration between environments, and keeping local development close to the
same API contract used by operations.

Provisioning manages control-plane configuration only. It registers repository sources, client identities, workspace variables, and input settings.
It does not publish a release by itself. Use Sync and Publish Release after a
source is registered.

## Resources

Each file contains one resource, a list of resources, or a `resources` array.

```yaml
resources:
  - apiVersion: windforce-lite.imprun.dev/v1
    kind: GitCredential
    metadata:
      name: app-git
    spec:
      method: pat
      storageRef: git/app/credential
      token:
        valueFrom:
          env: APP_GIT_TOKEN

  - apiVersion: windforce-lite.imprun.dev/v1
    kind: AppSource
    metadata:
      name: app-source
    spec:
      name: app-source
      repository:
        url: https://example.test/group/app.git
        branch: main
        subpath: apps/example
        authRef: app-git

  - apiVersion: windforce-lite.imprun.dev/v1
    kind: Client
    metadata:
      name: Example Client

  - apiVersion: windforce-lite.imprun.dev/v1
    kind: InputSettings
    metadata:
      name: example-proxy
    spec:
      appKey: EXAMPLE
      actionKey: "1000"
      clientRef: Example Client
      lockedKeys:
        - EXAMPLE_PROXY_URL
      config:
        EXAMPLE_PROXY_URL:
          valueFrom:
            env: EXAMPLE_PROXY_URL
```

## Value Sources

Fields that may contain sensitive or environment-specific values support
`valueFrom`.

```yaml
token:
  valueFrom:
    env: APP_GIT_TOKEN
```

```yaml
password:
  valueFrom:
    file: /run/secrets/app_git_password
```

Inline `value` is supported for non-secret local values. Exported sensitive
values are redacted and must be replaced with `valueFrom` before they can be
applied.

## Apply

Apply a file through the control-plane API:

```bash
python tools/windforce_control.py provision-import --file provisioning.yaml
```

Validate without writing state:

```bash
python tools/windforce_control.py provision-import --file provisioning.yaml --dry-run
```

Apply a directory at process startup by mounting it into the control-plane or
standalone process and setting:

```bash
WINDFORCE_LITE_PROVISION_DIR=/etc/windforce-lite/provisioning
```

The server reads `.json`, `.yaml`, and `.yml` files in lexical order.

## Export

Export the current workspace as provisioning resources:

```bash
python tools/windforce_control.py provision-export --format yaml
```

Client API tokens are not provisioning resources and are never exported. Issue or rotate them through the Client Registry control-plane API. Exported secret variables use redaction markers. Add `--include-values` only for local, non-secret review workflows.
