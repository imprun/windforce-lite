import type { WebhookDeliveryState } from "../lib/api";

const labels: Record<WebhookDeliveryState, string> = {
  pending: "Pending",
  delivering: "Delivering",
  retrying: "Retrying",
  succeeded: "Delivered",
  failed: "Failed",
  canceled: "Canceled",
};

export function WebhookSubscriptionStatus({ enabled, deleted }: { enabled: boolean; deleted?: boolean }) {
  if (deleted) return <span className="badge badge-neutral">Deleted</span>;
  if (!enabled) return <span className="badge badge-warning">Disabled</span>;
  return <span className="badge badge-good">Enabled</span>;
}

export function WebhookDeliveryStatus({ state }: { state: WebhookDeliveryState }) {
  const tone = state === "succeeded" ? "good" : state === "failed" ? "critical" : state === "canceled" ? "neutral" : state === "retrying" ? "warning" : "running";
  return <span className={`badge badge-${tone}`}>{labels[state]}</span>;
}

export function webhookEventLabel(type: string): string {
  if (type === "windforce.release.published") return "Release published";
  if (type === "windforce.webhook.test") return "Test delivery";
  return type;
}
