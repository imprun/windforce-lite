# Windforce Execution SDK for Python

This package lets protocol adapters create and observe Windforce runs through
the versioned Execution API. It does not access the Windforce database or
catalog files.

```python
from windforce_execution import WindforceExecutionClient

client = WindforceExecutionClient(
    "http://windforce-lite:8080",
    workspace="default",
    token="...",
)
run = client.create_run(
    app="example",
    action="lookup",
    input={"query": "value"},
    adapter="http",
    idempotency_key="request-123",
)
run = client.wait(run.run_id, timeout_seconds=60)
result = client.get_result(run.run_id)
```
