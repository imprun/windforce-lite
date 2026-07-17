import { KeyRound, Search, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import { ErrorNotice, Field, Modal, Panel } from "../components/ui";
import type { AppSummary, WebhookSubscription } from "../lib/api";
import { errorMessage, webhookAppKeys } from "../lib/api";
import { useApp } from "../lib/app-context";
import { formatTime } from "../lib/format";
import { WebhookSecretDialog } from "./WebhookSecretDialog";
import { WebhookSubscriptionStatus } from "./WebhookStatus";

type Props = {
  subscription: WebhookSubscription;
  apps: AppSummary[];
  onUpdated: (subscription: WebhookSubscription) => void;
  onDeleted: () => void;
};

export function WebhookOverview({ subscription, apps, onUpdated, onDeleted }: Props) {
  const { api, notify } = useApp();
  const [name, setName] = useState(subscription.name);
  const [endpoint, setEndpoint] = useState("");
  const [enabled, setEnabled] = useState(subscription.enabled);
  const [scope, setScope] = useState<"all" | "selected">(webhookAppKeys(subscription).length ? "selected" : "all");
  const [selectedApps, setSelectedApps] = useState(webhookAppKeys(subscription));
  const [search, setSearch] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [rotateOpen, setRotateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteConfirmation, setDeleteConfirmation] = useState("");
  const [secret, setSecret] = useState("");

  useEffect(() => {
    setName(subscription.name);
    setEndpoint("");
    setEnabled(subscription.enabled);
    setScope(webhookAppKeys(subscription).length ? "selected" : "all");
    setSelectedApps(webhookAppKeys(subscription));
  }, [subscription]);

  const availableApps = useMemo(() => {
    const byKey = new Map(apps.map((app) => [app.app_key, app]));
    for (const appKey of selectedApps) {
      if (!byKey.has(appKey)) {
        byKey.set(appKey, {
          id: appKey,
          workspace_id: subscription.workspace_id,
          app_key: appKey,
          git_source_id: 0,
          commit_sha: "",
          entrypoint: "App is not currently registered",
          tag: "",
          timeout_s: 0,
          script_lang: "",
          bundle_status: "missing",
          updated_at: subscription.updated_at,
          effective_route_tag: "",
          actions_count: 0,
        });
      }
    }
    const query = search.trim().toLowerCase();
    return [...byKey.values()]
      .filter((app) => !query || app.app_key.toLowerCase().includes(query))
      .sort((left, right) => left.app_key.localeCompare(right.app_key));
  }, [apps, search, selectedApps, subscription.updated_at, subscription.workspace_id]);

  function toggleApp(appKey: string) {
    setSelectedApps((current) =>
      current.includes(appKey) ? current.filter((key) => key !== appKey) : [...current, appKey].sort(),
    );
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const normalizedName = name.trim();
    const nextApps = scope === "all" ? [] : selectedApps;
    if (!normalizedName) {
      setError("Name is required.");
      return;
    }
    if (scope === "selected" && nextApps.length === 0) {
      setError("Select at least one app or change the scope to all apps.");
      return;
    }
    const payload: Parameters<typeof api.updateWebhookSubscription>[1] = {};
    if (normalizedName !== subscription.name) payload.name = normalizedName;
    if (endpoint.trim()) payload.endpoint = endpoint.trim();
    if (enabled !== subscription.enabled) payload.enabled = enabled;
    if (JSON.stringify(nextApps) !== JSON.stringify(webhookAppKeys(subscription))) payload.app_keys = nextApps;
    if (Object.keys(payload).length === 0) {
      notify("info", "No webhook settings changed.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.updateWebhookSubscription(subscription.id, payload);
      onUpdated(result.subscription);
      notify("ok", `Saved webhook ${result.subscription.name}.`);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function rotateSecret() {
    setBusy(true);
    setError("");
    try {
      const result = await api.updateWebhookSubscription(subscription.id, { rotate_signing_secret: true });
      setRotateOpen(false);
      onUpdated(result.subscription);
      setSecret(result.signing_secret || "");
      notify("ok", "Rotated the webhook signing secret.");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setError("");
    try {
      await api.deleteWebhookSubscription(subscription.id);
      notify("ok", `Deleted webhook ${subscription.name}.`);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  const deleted = Boolean(subscription.deleted_at);
  return (
    <div className="webhookDetailStack">
      <Panel title="Configuration" subtitle="Delivery scope and receiver settings used for future release events.">
        <div className="webhookConfigSummary">
          <div>
            <span className="fieldLabel">Status</span>
            <WebhookSubscriptionStatus enabled={subscription.enabled} deleted={deleted} />
          </div>
          <div>
            <span className="fieldLabel">Receiver</span>
            <strong className="mono">{subscription.endpoint_summary}</strong>
            <span className="fieldHint">The URL path is never returned by the API.</span>
          </div>
          <div>
            <span className="fieldLabel">Event</span>
            <strong>Release published</strong>
            <span className="fieldHint mono">windforce.release.published</span>
          </div>
          <div>
            <span className="fieldLabel">Last updated</span>
            <strong>{formatTime(subscription.updated_at)}</strong>
            <span className="fieldHint">by {subscription.updated_by || "system"}</span>
          </div>
        </div>

        {deleted ? (
          <div className="inlineNotice warning">Deleted webhooks are read-only. Delivery and audit history remain available.</div>
        ) : (
          <form className="webhookEditForm" onSubmit={save}>
            <div className="formGrid">
              <Field label="Name" hint="Shown in operations, delivery history, and audit events.">
                <input id="webhookEditName" maxLength={200} value={name} onChange={(event) => setName(event.target.value)} />
              </Field>
              <Field label="Replace endpoint URL" hint="Leave blank to keep the current receiver. Entering a value replaces the full URL.">
                <input
                  id="webhookEditEndpoint"
                  type="url"
                  value={endpoint}
                  onChange={(event) => setEndpoint(event.target.value)}
                  placeholder="https://hooks.example.com/windforce"
                  spellCheck={false}
                />
              </Field>
            </div>
            <label className="toggleField">
              <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
              <span>
                <strong>Enable deliveries</strong>
                <small>Disabled subscriptions keep their configuration and history but do not receive new events.</small>
              </span>
            </label>

            <fieldset className="webhookScopeFieldset">
              <legend>App scope</legend>
              <div className="segmented webhookScopeMode" role="group" aria-label="App scope">
                <button type="button" className={scope === "all" ? "segment active" : "segment"} onClick={() => setScope("all")}>All apps</button>
                <button type="button" className={scope === "selected" ? "segment active" : "segment"} onClick={() => setScope("selected")}>Selected apps</button>
              </div>
              {scope === "selected" ? (
                <div className="appScopePicker compact">
                  <label className="scopeSearch">
                    <Search size={16} aria-hidden="true" />
                    <input aria-label="Filter apps" placeholder="Filter apps…" value={search} onChange={(event) => setSearch(event.target.value)} />
                  </label>
                  <div className="appScopeList" id="webhookEditAppScope">
                    {availableApps.map((app) => (
                      <label className="appScopeOption" key={app.app_key}>
                        <input type="checkbox" checked={selectedApps.includes(app.app_key)} onChange={() => toggleApp(app.app_key)} />
                        <span><strong>{app.app_key}</strong><small>{app.entrypoint}</small></span>
                      </label>
                    ))}
                  </div>
                </div>
              ) : <p className="fieldHint">Receive release events for every app in this workspace.</p>}
            </fieldset>
            {error ? <ErrorNotice message={error} /> : null}
            <div className="formActions">
              <button className="button primary" type="submit" disabled={busy} id="saveWebhookButton">
                {busy ? "Saving…" : "Save changes"}
              </button>
            </div>
          </form>
        )}
      </Panel>

      <Panel title="Signing secret" subtitle="Receivers use this secret to verify that a request came from Windforce.">
        <div className="webhookSecurityRow">
          <div className="securityIdentity">
            <KeyRound size={18} aria-hidden="true" />
            <div>
              <strong>{subscription.has_signing_secret ? "Signing enabled" : "No signing secret"}</strong>
              <p>The stored secret cannot be read. Rotation immediately invalidates the current secret.</p>
            </div>
          </div>
          {!deleted ? <button className="button" type="button" onClick={() => setRotateOpen(true)}>Rotate secret</button> : null}
        </div>
      </Panel>

      {!deleted ? (
        <Panel title="Delete webhook" subtitle="Stop future deliveries while retaining delivery and audit history.">
          <div className="dangerZoneRow">
            <p>Deletion cannot be undone from the console.</p>
            <button className="button danger" type="button" onClick={() => setDeleteOpen(true)}>
              <Trash2 size={16} aria-hidden="true" />
              Delete webhook
            </button>
          </div>
        </Panel>
      ) : null}

      {rotateOpen ? (
        <Modal title="Rotate signing secret?" subtitle="The current receiver will fail verification until it is configured with the new secret." onClose={() => setRotateOpen(false)}>
          <div className="dialogFooter">
            <button className="button" type="button" onClick={() => setRotateOpen(false)}>Cancel</button>
            <button className="button primary" type="button" disabled={busy} onClick={rotateSecret}>{busy ? "Rotating…" : "Rotate secret"}</button>
          </div>
        </Modal>
      ) : null}
      {deleteOpen ? (
        <Modal title="Delete webhook?" subtitle="Future releases will no longer create deliveries for this receiver." onClose={() => setDeleteOpen(false)}>
          <Field label={`Type ${subscription.name} to confirm`}>
            <input autoFocus value={deleteConfirmation} onChange={(event) => setDeleteConfirmation(event.target.value)} />
          </Field>
          <div className="dialogFooter">
            <button className="button" type="button" onClick={() => setDeleteOpen(false)}>Cancel</button>
            <button className="button danger" type="button" disabled={busy || deleteConfirmation !== subscription.name} onClick={remove}>
              {busy ? "Deleting…" : "Delete webhook"}
            </button>
          </div>
        </Modal>
      ) : null}
      {secret ? <WebhookSecretDialog secret={secret} onClose={() => setSecret("")} /> : null}
    </div>
  );
}
