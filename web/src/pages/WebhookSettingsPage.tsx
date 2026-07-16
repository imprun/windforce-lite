import { Plus, RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { SettingsNav } from "../components/SettingsNav";
import { EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import { WebhookDeliveryStatus, WebhookSubscriptionStatus } from "../features/WebhookStatus";
import { webhookAppKeys, type WebhookDeliveryDetail, type WebhookSubscription } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, formatTime } from "../lib/format";
import { Link } from "../lib/router";

type WebhookRow = {
  subscription: WebhookSubscription;
  lastDelivery: WebhookDeliveryDetail | null;
};

export function WebhookSettingsPage() {
  const { api } = useApp();
  const [search, setSearch] = useState("");
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const state = useAsync(async () => {
    const subscriptions = await api.webhookSubscriptions(includeDeleted);
    return Promise.all(
      subscriptions.map(async (subscription): Promise<WebhookRow> => {
        try {
          const deliveries = await api.webhookDeliveries(subscription.id, { limit: 1 });
          return { subscription, lastDelivery: deliveries.items[0] || null };
        } catch {
          return { subscription, lastDelivery: null };
        }
      }),
    );
  }, [api, includeDeleted]);

  const rows = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!state.data || !query) return state.data || [];
    return state.data.filter(({ subscription }) =>
      [subscription.name, subscription.endpoint_summary, ...webhookAppKeys(subscription)].some((value) => value.toLowerCase().includes(query)),
    );
  }, [search, state.data]);

  const enabledCount = state.data?.filter(({ subscription }) => subscription.enabled && !subscription.deleted_at).length || 0;
  const failedCount = state.data?.filter(({ lastDelivery }) => lastDelivery?.delivery.state === "failed").length || 0;

  return (
    <Layout
      title="Webhooks"
      subtitle="Release notifications delivered from the control plane to signed HTTPS receivers."
      actions={
        <>
          <input
            className="searchInput"
            aria-label="Filter webhooks"
            placeholder="Filter webhooks…"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <button className="button" type="button" onClick={() => state.reload()} title="Refresh webhooks">
            <RefreshCw size={16} aria-hidden="true" />
            Refresh
          </button>
          <Link className="button primary" to="/settings/webhooks/new">
            <Plus size={16} aria-hidden="true" />
            Create webhook
          </Link>
        </>
      }
    >
      <SettingsNav />
      <div className="webhookSummaryBar" aria-label="Webhook summary">
        <span><strong>{enabledCount}</strong> enabled</span>
        <span className={failedCount ? "summaryCritical" : undefined}><strong>{failedCount}</strong> latest deliveries failed</span>
        <label className="historyToggle">
          <input type="checkbox" checked={includeDeleted} onChange={(event) => setIncludeDeleted(event.target.checked)} />
          Show deleted
        </label>
      </div>

      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading label="Loading webhooks…" /> : null}
      {state.data ? (
        <Panel
          title="Subscriptions"
          subtitle={`${rows.length} webhook${rows.length === 1 ? "" : "s"} in the current view`}
        >
          {rows.length === 0 ? (
            <EmptyState title={search ? "No webhooks match the filter." : "No webhook subscriptions yet."}>
              {!search ? <Link className="button primary" to="/settings/webhooks/new">Create webhook</Link> : null}
            </EmptyState>
          ) : (
            <div className="tableWrap">
              <table className="table webhookTable" id="webhookList">
                <thead>
                  <tr>
                    <th>Webhook</th>
                    <th>Endpoint</th>
                    <th>App scope</th>
                    <th>Last delivery</th>
                    <th>Updated</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map(({ subscription, lastDelivery }) => (
                    <tr key={subscription.id}>
                      <td>
                        <Link className="cellTitle" to={`/settings/webhooks/${subscription.id}`}>{subscription.name}</Link>
                        <span className="cellSub webhookStatusLine">
                          <WebhookSubscriptionStatus enabled={subscription.enabled} deleted={Boolean(subscription.deleted_at)} />
                        </span>
                      </td>
                      <td>
                        <span className="mono cellTitle">{subscription.endpoint_summary}</span>
                        <span className="cellSub">Endpoint path is hidden</span>
                      </td>
                      <td>
                        {webhookAppKeys(subscription).length === 0 ? (
                          <span className="cellTitle">All apps</span>
                        ) : (
                          <>
                            <span className="cellTitle">{webhookAppKeys(subscription).slice(0, 2).join(", ")}</span>
                            {webhookAppKeys(subscription).length > 2 ? <span className="cellSub">+{webhookAppKeys(subscription).length - 2} more</span> : null}
                          </>
                        )}
                      </td>
                      <td>
                        {lastDelivery ? (
                          <>
                            <WebhookDeliveryStatus state={lastDelivery.delivery.state} />
                            <span className="cellSub" title={formatTime(lastDelivery.delivery.created_at)}>{formatRelative(lastDelivery.delivery.created_at)}</span>
                          </>
                        ) : <span className="cellSub">No deliveries yet</span>}
                      </td>
                      <td title={formatTime(subscription.updated_at)}>
                        <span className="cellTitle">{formatRelative(subscription.updated_at)}</span>
                        <span className="cellSub">{subscription.updated_by}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      ) : null}
    </Layout>
  );
}
