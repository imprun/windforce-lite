import { ArrowLeft, Check, Search } from "lucide-react";
import { useMemo, useState, type FormEvent } from "react";
import { Layout } from "../components/Layout";
import { SettingsNav } from "../components/SettingsNav";
import { ErrorNotice, Field, Loading, Panel } from "../components/ui";
import { WebhookSecretDialog } from "../features/WebhookSecretDialog";
import { errorMessage, type WebhookSubscriptionMutation } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { Link, useRouter } from "../lib/router";

export function WebhookCreatePage() {
  const { api, notify } = useApp();
  const { navigate } = useRouter();
  const apps = useAsync(() => api.apps(), [api]);
  const [name, setName] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [scope, setScope] = useState<"all" | "selected">("all");
  const [selectedApps, setSelectedApps] = useState<string[]>([]);
  const [search, setSearch] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<WebhookSubscriptionMutation | null>(null);

  const visibleApps = useMemo(() => {
    const query = search.trim().toLowerCase();
    const rows = apps.data?.apps || [];
    return query ? rows.filter((app) => app.app_key.toLowerCase().includes(query)) : rows;
  }, [apps.data, search]);

  function toggleApp(appKey: string) {
    setSelectedApps((current) =>
      current.includes(appKey) ? current.filter((key) => key !== appKey) : [...current, appKey].sort(),
    );
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const normalizedName = name.trim();
    const normalizedEndpoint = endpoint.trim();
    if (!normalizedName || !normalizedEndpoint) {
      setError("Name and endpoint are required.");
      return;
    }
    if (scope === "selected" && selectedApps.length === 0) {
      setError("Select at least one app or change the scope to all apps.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.createWebhookSubscription({
        name: normalizedName,
        endpoint: normalizedEndpoint,
        event_types: ["windforce.release.published", "windforce.release.rolled_back"],
        app_keys: scope === "all" ? [] : selectedApps,
        enabled: true,
      });
      setCreated(result);
      notify("ok", `Created webhook ${result.subscription.name}.`);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  function finish() {
    if (created) navigate(`/settings/webhooks/${created.subscription.id}`);
  }

  return (
    <Layout
      title="Create webhook"
      subtitle="Send release events to one HTTPS receiver with a dedicated signing secret."
      actions={
        <Link className="button" to="/settings/webhooks">
          <ArrowLeft size={16} aria-hidden="true" />
          Back to webhooks
        </Link>
      }
    >
      <SettingsNav />
      <form className="webhookFormLayout" onSubmit={submit}>
        <Panel title="Receiver" subtitle="Windforce signs each event and delivers it asynchronously.">
          <div className="formGrid">
            <Field label="Name" hint="Use a name operators can recognize in delivery history and audit events.">
              <input
                id="webhookName"
                autoFocus
                maxLength={200}
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="Release notifications"
              />
            </Field>
            <Field label="Endpoint URL" hint="HTTPS is required outside explicitly allowed local development endpoints.">
              <input
                id="webhookEndpoint"
                type="url"
                value={endpoint}
                onChange={(event) => setEndpoint(event.target.value)}
                placeholder="https://hooks.example.com/windforce"
                spellCheck={false}
              />
            </Field>
          </div>
          <div className="webhookEventSummary">
            <span className="webhookEventIcon" aria-hidden="true"><Check size={15} /></span>
            <div>
              <strong>Release activity</strong>
              <p>Triggered after a worker-visible release is published or a historical release is activated.</p>
            </div>
          </div>
        </Panel>

        <Panel title="App scope" subtitle="Limit notifications to selected apps, or receive releases from the entire workspace.">
          <div className="segmented webhookScopeMode" role="group" aria-label="App scope">
            <button type="button" className={scope === "all" ? "segment active" : "segment"} onClick={() => setScope("all")}>
              All apps
            </button>
            <button type="button" className={scope === "selected" ? "segment active" : "segment"} onClick={() => setScope("selected")}>
              Selected apps
            </button>
          </div>
          {scope === "selected" ? (
            <div className="appScopePicker">
              <label className="scopeSearch">
                <Search size={16} aria-hidden="true" />
                <input aria-label="Filter apps" placeholder="Filter apps…" value={search} onChange={(event) => setSearch(event.target.value)} />
              </label>
              {apps.error ? <ErrorNotice message={apps.error} onRetry={apps.reload} /> : null}
              {apps.loading && !apps.data ? <Loading label="Loading apps…" /> : null}
              {apps.data ? (
                <div className="appScopeList" id="webhookAppScope">
                  {visibleApps.map((app) => (
                    <label className="appScopeOption" key={`${app.git_source_id}-${app.app_key}`}>
                      <input type="checkbox" checked={selectedApps.includes(app.app_key)} onChange={() => toggleApp(app.app_key)} />
                      <span>
                        <strong>{app.app_key}</strong>
                        <small>{app.entrypoint}</small>
                      </span>
                    </label>
                  ))}
                  {visibleApps.length === 0 ? <p className="fieldHint">No apps match the filter.</p> : null}
                </div>
              ) : null}
            </div>
          ) : (
            <p className="fieldHint">Every release published in this workspace will create a delivery.</p>
          )}
        </Panel>

        {error ? <ErrorNotice message={error} /> : null}
        <div className="formActions webhookFormActions">
          <Link className="button" to="/settings/webhooks">Cancel</Link>
          <button className="button primary" type="submit" disabled={busy} id="createWebhookButton">
            {busy ? "Creating…" : "Create webhook"}
          </button>
        </div>
      </form>

      {created?.signing_secret ? <WebhookSecretDialog secret={created.signing_secret} endpoint={endpoint} onClose={finish} /> : null}
    </Layout>
  );
}
