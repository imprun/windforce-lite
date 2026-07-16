import { AuditEventTable } from "./AuditEventTable";
import { ErrorNotice, Loading, Panel } from "../components/ui";
import { useApp, useAsync } from "../lib/app-context";

export function WebhookAudit({ subscriptionID }: { subscriptionID: string }) {
  const { api } = useApp();
  const state = useAsync(() => api.auditEvents({ category: "webhook", limit: 250 }), [api, subscriptionID]);
  const events = state.data?.filter((event) => event.webhook_subscription_id === subscriptionID) || [];

  return (
    <Panel
      title="Webhook audit"
      subtitle="Configuration, test, retry, and deletion decisions recorded for this subscription."
      actions={<button className="button" type="button" onClick={state.reload}>Refresh</button>}
    >
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading label="Loading audit events…" /> : null}
      {state.data ? <AuditEventTable events={events} emptyTitle="No audit events recorded for this webhook." /> : null}
    </Panel>
  );
}
