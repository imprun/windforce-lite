import { RotateCcw } from "lucide-react";
import { DefinitionList, JsonBlock, Sheet } from "../components/ui";
import type { WebhookDeliveryDetail } from "../lib/api";
import { formatTime } from "../lib/format";
import { WebhookDeliveryStatus, webhookEventLabel } from "./WebhookStatus";

export function WebhookDeliverySheet({
  detail,
  subscriptionActive,
  retrying,
  onRetry,
  onClose,
}: {
  detail: WebhookDeliveryDetail;
  subscriptionActive: boolean;
  retrying: boolean;
  onRetry: () => void;
  onClose: () => void;
}) {
  const { delivery, event } = detail;
  const canRetry = delivery.state === "failed" && subscriptionActive;
  return (
    <Sheet
      title="Delivery detail"
      subtitle={delivery.id}
      onClose={onClose}
      id="webhookDeliverySheet"
      actions={
        canRetry ? (
          <button className="button primary" type="button" disabled={retrying} onClick={onRetry}>
            <RotateCcw size={16} aria-hidden="true" />
            {retrying ? "Queuing retry…" : "Retry delivery"}
          </button>
        ) : (
          <span className="fieldHint">
            {delivery.state === "failed" ? "Enable the subscription before retrying." : "Only failed deliveries can be retried."}
          </span>
        )
      }
    >
      <div className="sheetSection deliveryIdentity">
        <WebhookDeliveryStatus state={delivery.state} />
        <strong>{webhookEventLabel(event.type)}</strong>
      </div>
      <div className="sheetSection">
        <h3>Attempt</h3>
        <DefinitionList
          className="sheetFacts"
          items={[
            ["Attempts", delivery.attempt],
            ["HTTP response", delivery.response_status ?? "—"],
            ["Latency", delivery.latency_ms != null ? `${delivery.latency_ms} ms` : "—"],
            ["Created", formatTime(delivery.created_at)],
            ["Completed", delivery.completed_at ? formatTime(delivery.completed_at) : "—"],
            ["Next attempt", delivery.state === "retrying" || delivery.state === "pending" ? formatTime(delivery.next_attempt_at) : "—"],
          ]}
        />
        {delivery.error_summary ? <div className="inlineNotice error">{delivery.error_summary}</div> : null}
      </div>
      <div className="sheetSection">
        <h3>Event</h3>
        <DefinitionList
          className="sheetFacts"
          items={[
            ["Event ID", <span className="mono">{event.id}</span>],
            ["Type", <span className="mono">{event.type}</span>],
            ["Occurred", formatTime(event.time)],
          ]}
        />
        <JsonBlock value={event.data} maxHeight={280} />
      </div>
    </Sheet>
  );
}
