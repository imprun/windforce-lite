import { ArrowLeft, RefreshCw, Send } from "lucide-react";
import { useState } from "react";
import { Layout } from "../components/Layout";
import { SettingsNav } from "../components/SettingsNav";
import { ErrorNotice, Loading } from "../components/ui";
import { WebhookAudit } from "../features/WebhookAudit";
import { WebhookDeliveries } from "../features/WebhookDeliveries";
import { WebhookOverview } from "../features/WebhookOverview";
import { WebhookSubscriptionStatus } from "../features/WebhookStatus";
import type { WebhookSubscription } from "../lib/api";
import { errorMessage } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { Link, useRouter } from "../lib/router";

const tabs = [
  { key: "overview", label: "Overview" },
  { key: "deliveries", label: "Deliveries" },
  { key: "audit", label: "Audit" },
] as const;

export function WebhookDetailPage({ subscriptionID, tab }: { subscriptionID: string; tab: string }) {
  const { api, notify } = useApp();
  const { navigate } = useRouter();
  const [updated, setUpdated] = useState<WebhookSubscription | null>(null);
  const [testing, setTesting] = useState(false);
  const [actionError, setActionError] = useState("");
  const state = useAsync(async () => {
    const [subscriptions, apps] = await Promise.all([api.webhookSubscriptions(true), api.apps()]);
    return {
      subscription: subscriptions.find((item) => item.id === subscriptionID) || null,
      apps: apps.apps || [],
    };
  }, [api, subscriptionID]);

  const subscription = updated || state.data?.subscription;
  const activeTab = tabs.some((item) => item.key === tab) ? tab : "overview";

  async function sendTest() {
    if (!subscription) return;
    setTesting(true);
    setActionError("");
    try {
      await api.testWebhookSubscription(subscription.id);
      notify("ok", "Created a signed test delivery.");
      navigate(`/settings/webhooks/${subscription.id}/deliveries`);
    } catch (cause) {
      setActionError(errorMessage(cause));
    } finally {
      setTesting(false);
    }
  }

  return (
    <Layout
      title={subscription?.name || "Webhook"}
      subtitle={subscription ? `${subscription.endpoint_summary} · Signed release delivery` : "Loading webhook configuration…"}
      actions={
        subscription ? (
          <>
            <WebhookSubscriptionStatus enabled={subscription.enabled} deleted={Boolean(subscription.deleted_at)} />
            <button className="button" type="button" title="Refresh webhook" onClick={() => { setUpdated(null); state.reload(); }}>
              <RefreshCw size={16} aria-hidden="true" />
              Refresh
            </button>
            {!subscription.deleted_at ? (
              <button className="button primary" type="button" disabled={testing || !subscription.enabled} onClick={sendTest}>
                <Send size={16} aria-hidden="true" />
                {testing ? "Sending…" : "Send test"}
              </button>
            ) : null}
          </>
        ) : null
      }
    >
      <SettingsNav />
      <div className="detailBreadcrumb">
        <Link to="/settings/webhooks"><ArrowLeft size={15} aria-hidden="true" /> Webhooks</Link>
        {subscription ? <span className="mono">{subscription.id}</span> : null}
      </div>
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {actionError ? <ErrorNotice message={actionError} /> : null}
      {state.loading && !state.data ? <Loading label="Loading webhook…" /> : null}
      {state.data && !subscription ? <ErrorNotice message="Webhook subscription not found." /> : null}
      {subscription ? (
        <>
          <nav className="tabBar webhookDetailTabs" aria-label="Webhook sections">
            {tabs.map((item) => (
              <Link
                key={item.key}
                className={activeTab === item.key ? "tab active" : "tab"}
                to={`/settings/webhooks/${subscription.id}${item.key === "overview" ? "" : `/${item.key}`}`}
              >
                {item.label}
              </Link>
            ))}
          </nav>
          {activeTab === "overview" ? (
            <WebhookOverview
              subscription={subscription}
              apps={state.data?.apps || []}
              onUpdated={setUpdated}
              onDeleted={() => navigate("/settings/webhooks")}
            />
          ) : null}
          {activeTab === "deliveries" ? <WebhookDeliveries subscription={subscription} /> : null}
          {activeTab === "audit" ? <WebhookAudit subscriptionID={subscription.id} /> : null}
        </>
      ) : null}
    </Layout>
  );
}
