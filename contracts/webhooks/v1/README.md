# Windforce Lite webhook contract v1

Windforce Lite sends structured CloudEvents to a registered webhook endpoint after a release transaction commits. Receivers must verify the signature against the raw request body before decoding JSON.

## Request

| Header | Value |
|---|---|
| `Content-Type` | `application/cloudevents+json` |
| `X-Windforce-Event` | Stable event ID from the body `id` field |
| `X-Windforce-Event-Type` | Event type from the body `type` field |
| `X-Windforce-Delivery` | Delivery record ID used for diagnostics |
| `X-Windforce-Timestamp` | Unix timestamp in seconds |
| `X-Windforce-Signature` | `v1=<hex HMAC-SHA256>` |

The signed bytes are:

```text
X-Windforce-Timestamp + "." + raw_request_body
```

Use a constant-time signature comparison and reject timestamps outside a short replay window. The public Go verifier is available at `github.com/imprun/windforce-lite/pkg/webhook`. The fixed cross-language input and expected digest are in `signature.example.json`.

## Delivery behavior

- Return any `2xx` only after the event is accepted for processing.
- Return `429` or `5xx` for retryable receiver or messenger failures.
- Other `4xx` responses end delivery without an automatic retry.
- The same event can be delivered more than once. Persist the event `id` as the side-effect idempotency key.
- Do not use the delivery ID for business idempotency; it identifies Windforce delivery records and retries.

`control-plane-event.schema.json` is the v1 schema. The release and test fixtures contain non-sensitive example values.
Receivers may reject unknown fields. A breaking payload change therefore requires a new contract directory and event type instead of changing v1 in place.

## Connector boundary

A messenger connector verifies this contract, deduplicates event IDs durably, maps `windforce.release.published` fields into its message format, and owns messenger credentials, rate limits, templates, and delivery state. Windforce Lite does not store Slack or Telegram credentials and does not render provider-specific payloads.

For local contract checks, run the generic receiver from the repository root:

```powershell
$env:WINDFORCE_WEBHOOK_SECRET = "replace-with-a-local-secret"
go run ./examples/webhook-receiver --addr 127.0.0.1:19090
```

Register `http://127.0.0.1:19090/webhook` only with the local loopback webhook option enabled. `GET /events` returns sanitized summaries accepted by this local receiver.
The example stores deduplication state in memory and must not be exposed as a production connector.
