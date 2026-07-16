import { useState, type FormEvent } from "react";
import { Layout } from "../components/Layout";
import { ErrorNotice, Loading, Panel } from "../components/ui";
import { AuditEventTable } from "../features/AuditEventTable";
import { useApp, useAsync } from "../lib/app-context";

export function AuditPage() {
  const { api } = useApp();
  const [category, setCategory] = useState("");
  const [appKey, setAppKey] = useState("");
  const [clientID, setClientID] = useState("");
  const [actorDraft, setActorDraft] = useState("");
  const [actor, setActor] = useState("");

  const options = useAsync(
    async () => {
      const [apps, clients] = await Promise.all([api.apps(), api.clients()]);
      return { apps: apps.apps || [], clients };
    },
    [api],
  );
  const selectedApp = options.data?.apps.find((app) => app.app_key === appKey);
  const events = useAsync(
    () => api.auditEvents({
      category,
      appKey,
      clientID,
      actor,
      gitSourceID: selectedApp?.git_source_id,
      limit: 250,
    }),
    [api, category, appKey, clientID, actor, selectedApp?.git_source_id],
  );

  function applyActor(event: FormEvent) {
    event.preventDefault();
    setActor(actorDraft.trim());
  }

  function resetFilters() {
    setCategory("");
    setAppKey("");
    setClientID("");
    setActorDraft("");
    setActor("");
  }

  const filtered = Boolean(category || appKey || clientID || actor);

  return (
    <Layout
      title="Audit"
      subtitle="Workspace change history across repositories, releases, clients, input settings, and webhooks."
      actions={
        <button className="button" type="button" onClick={() => events.reload()}>
          Refresh
        </button>
      }
    >
      <Panel
        title="Workspace activity"
        subtitle={events.data ? `${events.data.length} most recent event${events.data.length === 1 ? "" : "s"}` : "Loading events…"}
        actions={filtered ? <button className="button small" type="button" onClick={resetFilters}>Reset filters</button> : null}
      >
        <form className="auditFilters" onSubmit={applyActor}>
          <label className="filterField">
            <span>Category</span>
            <select value={category} onChange={(event) => setCategory(event.target.value)}>
              <option value="">All categories</option>
              <option value="repository">Repository</option>
              <option value="release">Release</option>
              <option value="client">Client Registry</option>
              <option value="input_settings">Input Settings</option>
              <option value="webhook">Webhooks</option>
            </select>
          </label>
          <label className="filterField">
            <span>App</span>
            <select value={appKey} onChange={(event) => setAppKey(event.target.value)}>
              <option value="">All apps</option>
              {(options.data?.apps || []).map((app) => (
                <option key={`${app.git_source_id}-${app.app_key}`} value={app.app_key}>{app.app_key}</option>
              ))}
            </select>
          </label>
          <label className="filterField">
            <span>Client</span>
            <select value={clientID} onChange={(event) => setClientID(event.target.value)}>
              <option value="">All clients</option>
              {(options.data?.clients || []).map((client) => (
                <option key={client.id} value={client.id}>{client.name}</option>
              ))}
            </select>
          </label>
          <label className="filterField auditActorFilter">
            <span>Actor</span>
            <span className="filterInputAction">
              <input value={actorDraft} placeholder="Name or account" onChange={(event) => setActorDraft(event.target.value)} />
              <button className="button" type="submit">Apply</button>
            </span>
          </label>
        </form>

        {options.error ? <ErrorNotice message={options.error} onRetry={options.reload} /> : null}
        {events.error ? <ErrorNotice message={events.error} onRetry={events.reload} /> : null}
        {events.loading && !events.data ? <Loading /> : null}
        {events.data ? <AuditEventTable events={events.data} /> : null}
      </Panel>
    </Layout>
  );
}
