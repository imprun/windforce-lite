# Windforce Execution SDK for Python

This package lets trusted protocol adapters create and observe Windforce runs through the versioned Execution API. It does not access the Windforce database or catalog files.

```python
from windforce_execution import WindforceExecutionClient

client = WindforceExecutionClient(
    "http://windforce-core:8080",
    workspace="default",
    token="...",
)
run = client.create_run(
    app="example",
    action="lookup",
    input={"query": "value"},
    client_id="client_01",
    adapter="http",
    idempotency_key="request-123",
)
run = client.wait(run.run_id, timeout_seconds=60)
result = client.get_result(run.run_id)
```

`client_id` is an optional Client Registry identity asserted by a trusted trigger adapter. It selects client-scoped input settings and is not a Windforce API credential. External callers use their one-time-issued `wfk_` bearer token with the public HTTP trigger API instead.
