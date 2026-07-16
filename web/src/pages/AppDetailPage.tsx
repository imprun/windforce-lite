import { useState } from "react";
import { Layout } from "../components/Layout";
import { ReleaseMarkdown } from "../components/ReleaseMarkdown";
import {
  DefinitionList,
  EmptyState,
  ErrorNotice,
  JsonBlock,
  Loading,
  Panel,
  ReleaseStateBadge,
} from "../components/ui";
import { StatTile, WindowSelector, windowLabel } from "../components/stats";
import { PublishReleaseDialog } from "../features/PublishReleaseDialog";
import { RepositorySettings } from "../features/RepositorySettings";
import { AppInputSettings } from "../features/AppInputSettings";
import { AuditEventTable } from "../features/AuditEventTable";
import {
  type ActionView,
  type ActionSchemas,
  type AppDetail,
  type AppDocumentation,
  type AppSummary,
  type GitSource,
} from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp, useAsync } from "../lib/app-context";
import { formatJSON, formatRelative, formatTime, shortSHA } from "../lib/format";
import { forgeCommitURL, forgeName, forgeTreeURL } from "../lib/repo";
import { Link, useRouter } from "../lib/router";
import { describeSchema, formatSchemaValue, type SchemaField } from "../lib/schema-document";

const tabs = [
  { key: "overview", label: "Overview" },
  { key: "docs", label: "Docs" },
  { key: "input-settings", label: "Input Settings" },
  { key: "monitoring", label: "Monitoring" },
  { key: "repository", label: "Repository" },
  { key: "releases", label: "Releases" },
  { key: "audit", label: "Audit" },
] as const;

type TabKey = (typeof tabs)[number]["key"];

export function AppDetailPage({
  sourceID,
  tab,
  section,
  actionKey,
}: {
  sourceID: number;
  tab: string;
  section?: string;
  actionKey?: string;
}) {
  const { api } = useApp();
  const { navigate } = useRouter();
  const [publishing, setPublishing] = useState(false);
  const [releaseHistoryRevision, setReleaseHistoryRevision] = useState(0);

  const activeTab: TabKey = (tabs.find((item) => item.key === tab)?.key || "overview") as TabKey;

  const state = useAsync(
    async () => {
      const [sources, apps] = await Promise.all([api.gitSources(), api.apps()]);
      const source = sources.find((item) => item.id === sourceID) || null;
      const app = (apps.apps || []).find((item) => item.git_source_id === sourceID) || null;
      const detail = app ? await api.app(app.app_key) : null;
      return { source, app, detail };
    },
    [api, sourceID],
  );

  const source = state.data?.source || null;
  const app = state.data?.app || null;
  const detail = state.data?.detail || null;

  if (state.loading && !state.data) {
    return (
      <Layout title="App">
        <Loading />
      </Layout>
    );
  }

  if (state.error) {
    return (
      <Layout title="App">
        <ErrorNotice message={state.error} onRetry={state.reload} />
      </Layout>
    );
  }

  if (!source && !app) {
    return (
      <Layout title="App not found">
        <EmptyState title="This app is not registered in the current workspace.">
          <Link className="button" to="/">
            Back to Apps
          </Link>
        </EmptyState>
      </Layout>
    );
  }

  // A released app can outlive its repository source registration: the
  // catalog contract stays after DELETE /git_sources. Repository settings
  // and publishing then have nothing to operate on.
  const visibleTabs = source ? tabs : tabs.filter((item) => item.key !== "repository");

  const title = app ? app.app_key : source!.name;
  return (
    <Layout
      title={title}
      subtitle={
        source
          ? `Repository source #${source.id}${source.name !== title ? ` · ${source.name}` : ""} · ${source.repo_url}`
          : "Repository source removed · the released contract is still active"
      }
      actions={
        <>
          <ReleaseStateBadge released={Boolean(app || source?.last_synced_commit)} />
          <button className="button" type="button" onClick={() => state.reload()}>
            Refresh
          </button>
          {source ? (
            <button className="button primary" type="button" id="publishReleaseButton" onClick={() => setPublishing(true)}>
              Publish Release
            </button>
          ) : null}
        </>
      }
    >
      <nav className="tabBar" aria-label="App detail tabs">
        {visibleTabs.map((item) => (
          <Link
            key={item.key}
            className={item.key === activeTab ? "tab active" : "tab"}
            to={item.key === "overview" ? `/apps/${sourceID}` : `/apps/${sourceID}/${item.key}`}
          >
            {item.label}
          </Link>
        ))}
      </nav>

      {activeTab === "overview" ? (
        <OverviewTab sourceID={sourceID} source={source} app={app} detail={detail} onPublish={() => setPublishing(true)} />
      ) : null}
      {activeTab === "docs" ? (
        <DocsTab
          sourceID={sourceID}
          source={source}
          app={app}
          detail={detail}
          section={section}
          actionKey={actionKey}
        />
      ) : null}
      {activeTab === "input-settings" && detail ? (
        <AppInputSettings
          detail={detail}
          sourceID={sourceID}
          selectedClientID={section === "client" ? actionKey : undefined}
        />
      ) : null}
      {activeTab === "monitoring" ? <MonitoringTab app={app} /> : null}
      {activeTab === "repository" && source ? <RepositorySettings source={source} onChanged={state.reload} /> : null}
      {activeTab === "releases" ? (
        <ReleasesTab
          appKey={app ? app.app_key : source!.name}
          released={Boolean(app)}
          repoURL={source?.repo_url || ""}
          refreshRevision={releaseHistoryRevision}
        />
      ) : null}
      {activeTab === "audit" ? <AuditTab sourceID={sourceID} appKey={app?.app_key || source?.name || ""} /> : null}
      {publishing && source ? (
        <PublishReleaseDialog
          source={source}
          appKey={app?.app_key}
          onClose={() => setPublishing(false)}
          onPublished={() => {
            setPublishing(false);
            setReleaseHistoryRevision((current) => current + 1);
            state.reload();
            navigate(`/apps/${source.id}/releases`);
          }}
        />
      ) : null}
    </Layout>
  );
}

function OverviewTab({
  sourceID,
  source,
  app,
  detail,
  onPublish,
}: {
  sourceID: number;
  source: GitSource | null;
  app: AppSummary | null;
  detail: AppDetail | null;
  onPublish: () => void;
}) {
  const { api } = useApp();
  const summary = useAsync(() => api.jobsSummary(), [api]);

  if (!app || !detail) {
    return (
      <Panel title="Active contract" subtitle="What workers can execute right now.">
        <EmptyState title="No release published yet.">
          <p>
            This repository source is registered but has no worker-visible contract. Publish a release to validate the
            source at HEAD and expose its actions to workers.
          </p>
          <button className="button primary" type="button" onClick={onPublish}>
            Publish Release
          </button>
        </EmptyState>
      </Panel>
    );
  }

  const routeTag = app.effective_route_tag || app.tag;
  const tagSummary = summary.data?.by_tag?.find((item) => item.tag === routeTag);
  const tagActivity = summary.error
    ? "unavailable"
    : summary.loading
      ? "checking…"
      : tagSummary
        ? `${tagSummary.queued_count} queued · ${tagSummary.running_count} running · ${tagSummary.completed_count_recent} completed in 24h`
        : "no recent jobs on this tag";

  return (
    <>
      <Panel title="Active release" subtitle="The source, routing, and execution settings selected for workers.">
        <div className="releaseSummary">
          <div className="releaseIdentity">
            <p className="eyebrow">Release commit</p>
            <p className="releaseCommit">
              <CommitRef repoURL={source?.repo_url || ""} commit={app.commit_sha} />
            </p>
            <p className="cellSub">Updated {formatRelative(app.updated_at)}</p>
          </div>
          <DefinitionList
            className="overviewFacts"
            items={[
              [
                "Source code",
                <SourceCodeRef
                  repoURL={source?.repo_url || ""}
                  commit={app.commit_sha}
                  subpath={source?.subpath || ""}
                />,
              ],
              ["Entrypoint", <span className="mono">{app.entrypoint}</span>],
              ["Script language", app.script_lang],
              ["Route tag", <span className="mono">{routeTag}</span>],
              [
                "Execution",
                `${app.timeout_s}s${app.required_capabilities?.length ? ` · ${app.required_capabilities.join(", ")}` : ""}`,
              ],
              ["API reference", <Link to={`/apps/${sourceID}/docs/reference`}>{detail.actions.length} action(s)</Link>],
            ]}
          />
        </div>
      </Panel>

      <Panel title="Readiness" subtitle="Current source and route signals for this release.">
        <DefinitionList
          className="readinessFacts"
          items={[
            ["Registered", source ? formatTime(source.created_at) : "repository source removed"],
            [
              "Last release",
              source?.last_synced_at
                ? `${formatTime(source.last_synced_at)} (${formatRelative(source.last_synced_at)})`
                : `${formatTime(app.updated_at)} (${formatRelative(app.updated_at)})`,
            ],
            [`Jobs on route tag ${routeTag}`, tagActivity],
          ]}
        />
      </Panel>
    </>
  );
}

function ReleasesTab({
  appKey,
  released,
  repoURL,
  refreshRevision,
}: {
  appKey: string;
  released: boolean;
  repoURL: string;
  refreshRevision: number;
}) {
  const { api } = useApp();
  const state = useAsync(async () => (released ? api.appHistory(appKey) : Promise.resolve([])), [
    api,
    appKey,
    released,
    refreshRevision,
  ]);

  return (
    <Panel title="Release history" subtitle="Who published which worker-visible contract, and why. Configuration changes are on the Audit tab.">
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading ? <Loading /> : null}
      {state.data && state.data.length === 0 ? (
        <EmptyState title="No releases recorded yet." />
      ) : null}
      {state.data && state.data.length > 0 ? (
        <div className="tableWrap">
          <table className="table" id="releaseHistory">
            <thead>
              <tr>
                <th>When</th>
                <th>Actor</th>
                <th>Commit</th>
                <th>Source</th>
                <th title="Unique identifier for this publish operation">Publish ID</th>
                <th>Note</th>
              </tr>
            </thead>
            <tbody>
              {state.data.map((item) => (
                <tr key={item.id}>
                  <td>
                    <span className="cellTitle">{formatRelative(item.created_at)}</span>
                    <span className="cellSub">{formatTime(item.created_at)}</span>
                  </td>
                  <td>{item.created_by || "system"}</td>
                  <td>
                    <CommitRef repoURL={repoURL} commit={item.commit_sha} />
                  </td>
                  <td>{item.source}</td>
                  <td className="mono">
                    {item.deployment_id ? (
                      <span title={item.deployment_id} aria-label={`Publish ID ${item.deployment_id}`}>
                        {shortSHA(item.deployment_id, 12)}
                      </span>
                    ) : (
                      "—"
                    )}
                  </td>
                  <td>{item.message || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </Panel>
  );
}

function DocsTab({
  sourceID,
  source,
  app,
  detail,
  section,
  actionKey,
}: {
  sourceID: number;
  source: GitSource | null;
  app: AppSummary | null;
  detail: AppDetail | null;
  section?: string;
  actionKey?: string;
}) {
  if (!app || !detail) {
    return (
      <Panel title="Documentation" subtitle="Guide and action schemas from the active release.">
        <EmptyState title="No release published yet.">
          <p>Publish a release first. Documentation is generated from that immutable source snapshot.</p>
        </EmptyState>
      </Panel>
    );
  }

  const activeSection = section === "actions" ? section : "guide";
  const actions = sortActions(detail.actions);
  const selectedAction = activeSection === "actions" ? actions.find((item) => item.action_key === actionKey) || null : null;
  return (
    <Panel title="Documentation" subtitle="Guide and action schemas from the active release.">
      <div className="docsLayout">
        <aside className="docsNav" aria-label="Documentation navigation">
          <p className="docsNavTitle">Docs</p>
          <Link
            className={activeSection === "guide" ? "docsNavLink active" : "docsNavLink"}
            to={`/apps/${sourceID}/docs`}
          >
            Guide
          </Link>
          <p className="docsNavTitle">Actions</p>
          <Link className={activeSection === "actions" && !actionKey ? "docsNavLink active" : "docsNavLink"} to={`/apps/${sourceID}/docs/actions`}>
            All actions
          </Link>
          {actions.map((action) => (
            <Link
              key={action.action_key}
              className={
                action.action_key === actionKey ? "docsNavLink docsNavAction active" : "docsNavLink docsNavAction"
              }
              to={`/apps/${sourceID}/docs/actions/${encodeURIComponent(action.action_key)}`}
            >
              <ActionLabel action={action} />
            </Link>
          ))}
        </aside>
        <section className="docsMain">
          {activeSection === "guide" ? <GuideDocument source={source} app={app} /> : null}
          {activeSection === "actions" && !actionKey ? <ActionReferenceList sourceID={sourceID} actions={actions} /> : null}
          {activeSection === "actions" && selectedAction ? (
            <ActionReferenceDetail app={app} action={selectedAction} />
          ) : null}
          {activeSection === "actions" && !selectedAction ? <EmptyState title="Action not found in the active release." /> : null}
        </section>
      </div>
    </Panel>
  );
}

function GuideDocument({ source, app }: { source: GitSource | null; app: AppSummary }) {
  const { api } = useApp();
  const documentation = useAsync(() => api.appDocumentation(app.app_key), [api, app.app_key]);
  if (documentation.loading && !documentation.data) return <Loading />;
  if (documentation.error) return <ErrorNotice message={documentation.error} onRetry={documentation.reload} />;
  return <RenderedGuide documentation={documentation.data} source={source} />;
}

function RenderedGuide({ documentation, source }: { documentation: AppDocumentation | null; source: GitSource | null }) {
  if (!documentation?.available || !documentation.markdown) {
    return (
      <EmptyState title="No README.md in the active release.">
        <p>Add a UTF-8 README.md to the app source, then publish a release.</p>
      </EmptyState>
    );
  }
  return (
    <article className="docsArticle">
      <header className="docsHeader">
        <h2>Guide</h2>
        <p>
          {documentation.path || "README.md"} pinned to release <span className="mono">{shortSHA(documentation.commit_sha, 12)}</span>.
        </p>
      </header>
      <ReleaseMarkdown
        markdown={documentation.markdown}
        repoURL={source?.repo_url || ""}
        commit={documentation.commit_sha}
        subpath={source?.subpath || ""}
      />
    </article>
  );
}

function ActionReferenceList({ sourceID, actions }: { sourceID: number; actions: ActionView[] }) {
  return (
    <section className="docsArticle">
      <header className="docsHeader">
        <div className="docsHeaderRow">
          <div>
            <h2>Actions</h2>
            <p>Select an action to review its request and result schemas.</p>
          </div>
        </div>
      </header>
      {actions.length === 0 ? (
        <EmptyState title="No actions in the active release." />
      ) : (
        <div className="docsActionList">
          {actions.map((action) => (
            <Link key={action.action_key} className="docsActionRow" to={`/apps/${sourceID}/docs/actions/${encodeURIComponent(action.action_key)}`}>
              <ActionLabel action={action} />
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}

function ActionReferenceDetail({ app, action }: { app: AppSummary; action: ActionView }) {
  const { api } = useApp();
  const schemas = useAsync(() => api.actionSchemas(app.app_key, action.action_key), [api, app.app_key, action.action_key]);
  const name = actionDisplayName(action.display_name);
  return (
    <article className="docsArticle">
      <header className="docsHeader">
        <div className="docsHeaderRow">
          <div>
            <h2>{name || `Action ${action.action_key}`}</h2>
            <p>
              Action key <span className="mono">{action.action_key}</span>
            </p>
          </div>
        </div>
      </header>
      {schemas.error ? <ErrorNotice message={schemas.error} onRetry={schemas.reload} /> : null}
      <SchemaReference schemas={schemas.data} loading={schemas.loading} appKey={app.app_key} actionKey={action.action_key} />
    </article>
  );
}

function ActionLabel({ action }: { action: ActionView }) {
  const displayName = actionDisplayName(action.display_name);
  return (
    <span className="actionLabel">
      <span className="actionLabelName">{displayName || `Action ${action.action_key}`}</span>
      <span className="actionLabelKey mono">Action key {action.action_key}</span>
    </span>
  );
}

function sortActions(actions: ActionView[]): ActionView[] {
  return [...actions].sort((left, right) => compareActionKeys(left.action_key, right.action_key));
}

function compareActionKeys(left: string, right: string): number {
  const numeric = /^\d+$/;
  const leftNumeric = numeric.test(left);
  const rightNumeric = numeric.test(right);
  if (leftNumeric && rightNumeric) {
    const normalizedLeft = left.replace(/^0+/, "") || "0";
    const normalizedRight = right.replace(/^0+/, "") || "0";
    if (normalizedLeft.length !== normalizedRight.length) return normalizedLeft.length - normalizedRight.length;
    return normalizedLeft < normalizedRight ? -1 : normalizedLeft > normalizedRight ? 1 : left.localeCompare(right);
  }
  if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
  return left.localeCompare(right);
}

function SchemaReference({
  schemas,
  loading,
  appKey,
  actionKey,
}: {
  schemas: ActionSchemas | null;
  loading: boolean;
  appKey: string;
  actionKey: string;
}) {
  if (loading && !schemas) return <Loading />;
  if (!schemas) return null;
  return (
    <div className="schemaStack">
      <SchemaSection
        title="Request body"
        emptyMessage="This request schema has no named fields. The action accepts an unconstrained JSON value."
        exampleLabel="Example request"
        filename={schemaFilename(appKey, actionKey, "input")}
        schema={schemas.input_schema}
      />
      <SchemaSection
        title="Result payload"
        emptyMessage="This result schema has no named fields. The action returns an unconstrained JSON value."
        exampleLabel="Example result"
        filename={schemaFilename(appKey, actionKey, "output")}
        schema={schemas.output_schema}
      />
    </div>
  );
}

function SchemaSection({
  title,
  emptyMessage,
  exampleLabel,
  filename,
  schema,
}: {
  title: string;
  emptyMessage: string;
  exampleLabel: string;
  filename: string;
  schema: unknown;
}) {
  const document = describeSchema(schema);
  return (
    <section className="schemaSection">
      <header className="schemaSectionHeader">
        <div>
          <h3>{title}</h3>
        </div>
        <div className="schemaSectionActions">
          <span className="schemaType mono">{document.type}</span>
          <SchemaArtifactControls filename={filename} schema={schema} />
        </div>
      </header>
      {document.fields.length > 0 ? <SchemaFieldTable fields={document.fields} /> : <p className="schemaEmpty">{emptyMessage}</p>}
      <div className="schemaExample">
        <div className="schemaExampleHeader">
          <h4>{exampleLabel}</h4>
          <span className="cellSub">{document.example.source === "declared" ? "Declared in schema" : "Generated from schema"}</span>
        </div>
        <JsonBlock value={formatJSON(document.example.value)} maxHeight={360} />
      </div>
      <details className="schemaSource">
        <summary>Raw JSON Schema</summary>
        <JsonBlock value={formatJSON(schema)} maxHeight={480} />
      </details>
    </section>
  );
}

function SchemaArtifactControls({ filename, schema }: { filename: string; schema: unknown }) {
  const { notify } = useApp();
  const [copied, setCopied] = useState(false);
  const text = formatJSON(schema);

  const handleCopy = async () => {
    try {
      await copyText(text);
      setCopied(true);
      notify("ok", "JSON Schema copied.");
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      notify("error", "Could not copy JSON Schema.");
    }
  };

  const handleDownload = () => {
    const blob = new Blob([text], { type: "application/schema+json;charset=utf-8" });
    const objectURL = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = filename;
    document.body.append(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(objectURL), 0);
  };

  return (
    <div className="schemaArtifactControls">
      <button className="button small" type="button" onClick={() => void handleCopy()}>
        {copied ? "Copied" : "Copy JSON"}
      </button>
      <button className="button small" type="button" onClick={handleDownload}>
        Download JSON
      </button>
    </div>
  );
}

async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textArea = document.createElement("textarea");
  textArea.value = text;
  textArea.setAttribute("readonly", "");
  textArea.style.position = "fixed";
  textArea.style.opacity = "0";
  document.body.append(textArea);
  textArea.select();
  const copied = document.execCommand("copy");
  textArea.remove();
  if (!copied) throw new Error("clipboard unavailable");
}

function schemaFilename(appKey: string, actionKey: string, kind: "input" | "output"): string {
  const part = (value: string) => value.replace(/[^a-z0-9._-]+/giu, "_");
  return `${part(appKey)}-${part(actionKey)}-${kind}.schema.json`;
}

function SchemaFieldTable({ fields }: { fields: SchemaField[] }) {
  return (
    <div className="tableWrap schemaTableWrap">
      <table className="table schemaTable">
        <thead>
          <tr>
            <th>Field</th>
            <th>Description</th>
            <th>Constraints</th>
          </tr>
        </thead>
        <tbody>
          {fields.map((field) => (
            <tr key={field.name}>
              <td>
                {field.title ? <span className="cellTitle">{field.title}</span> : null}
                <div className="schemaFieldIdentity">
                  <span className="mono">{field.name}</span>
                  <span className="schemaFieldType mono">{field.format ? `${field.type} (${field.format})` : field.type}</span>
                  {field.required ? <span className="badge badge-good">Required</span> : <span className="cellSub">Optional</span>}
                </div>
              </td>
              <td>{field.description || "—"}</td>
              <td><SchemaFieldValues field={field} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SchemaFieldValues({ field }: { field: SchemaField }) {
  const values: Array<[string, unknown]> = [];
  if (field.constValue !== undefined) values.push(["Fixed", field.constValue]);
  if (field.enumValues?.length) values.push(["Allowed", field.enumValues]);
  if (field.hasDefault) values.push(["Default", field.defaultValue]);
  if (values.length === 0) return <span>—</span>;
  return (
    <div className="schemaFieldValues">
      {values.map(([label, value]) => (
        <span key={label}>
          <span className="schemaValueLabel">{label}</span> <span className="mono">{formatSchemaValue(value)}</span>
        </span>
      ))}
    </div>
  );
}

// Commit reference: linked to the forge commit page when the repo host is
// GitHub/GitLab, plain text otherwise.
function CommitRef({ repoURL, commit }: { repoURL: string; commit: string | null | undefined }) {
  if (!commit) return <span>—</span>;
  const url = forgeCommitURL(repoURL, commit);
  if (!url) return <span className="mono">{shortSHA(commit, 12)}</span>;
  return (
    <a className="mono" href={url} target="_blank" rel="noreferrer">
      {shortSHA(commit, 12)}
    </a>
  );
}

// The UI does not mirror app source; it links to the repository host at the
// pinned release commit (ADR 0006).
function SourceCodeRef({
  repoURL,
  commit,
  subpath,
}: {
  repoURL: string;
  commit: string | null | undefined;
  subpath: string;
}) {
  const url = forgeTreeURL(repoURL, commit, subpath);
  if (url) {
    return (
      <a href={url} target="_blank" rel="noreferrer">
        Browse {subpath || "repository"} at {shortSHA(commit, 10)} on {forgeName(repoURL)}
      </a>
    );
  }
  if (!repoURL) return <span>repository source removed</span>;
  return (
    <span className="mono">
      {repoURL}
      {subpath ? ` · ${subpath}` : ""}
    </span>
  );
}

// Per-app slice of the workspace job aggregates (ADR 0005): the same
// summary endpoint, narrowed to this app's activity.
function MonitoringTab({ app }: { app: AppSummary | null }) {
  const { api } = useApp();
  const [windowSeconds, setWindowSeconds] = useState<number>(86400);
  const summary = useAsync(() => api.jobsSummary(windowSeconds), [api, windowSeconds]);

  if (!app) {
    return (
      <Panel title="Monitoring" subtitle="Aggregate job activity for this app.">
        <EmptyState title="No release published yet, so there is no job activity." />
      </Panel>
    );
  }

  const counts = summary.data?.by_app?.find((item) => item.app_key === app.app_key);
  const label = windowLabel(windowSeconds);
  const settled = counts ? counts.completed_count_recent + counts.failed_count_recent : 0;
  const failurePercent = counts && settled > 0 ? (counts.failed_count_recent / settled) * 100 : null;
  const failureRate =
    failurePercent === null ? "—" : `${failurePercent.toFixed(failurePercent > 0 && failurePercent < 1 ? 1 : 0)}%`;

  return (
    <Panel
      title="Monitoring"
      subtitle={`Aggregate job activity for ${app.app_key}. Individual runs live in the control-plane API and CLI.`}
      actions={<WindowSelector value={windowSeconds} onChange={setWindowSeconds} />}
    >
      {summary.error ? <ErrorNotice message={summary.error} onRetry={summary.reload} /> : null}
      {summary.loading && !summary.data ? <Loading /> : null}
      {summary.data ? (
        <div className="statRow" id="appMonitoring">
          <StatTile label="Queued" value={counts?.queued_count ?? 0} tone="waiting" />
          <StatTile label="Running" value={counts?.running_count ?? 0} tone="running" />
          <StatTile label={`Completed · ${label}`} value={counts?.completed_count_recent ?? 0} tone="good" />
          <StatTile label={`Failed · ${label}`} value={counts?.failed_count_recent ?? 0} tone="critical" />
          <StatTile label={`Canceled · ${label}`} value={counts?.canceled_count_recent ?? 0} tone="serious" />
          <StatTile label={`Failure rate · ${label}`} value={failureRate} tone="neutral" />
        </div>
      ) : null}
      {summary.data && !counts ? (
        <p className="cellSub">No job activity for this app in the selected window.</p>
      ) : null}
    </Panel>
  );
}

function AuditTab({ sourceID, appKey }: { sourceID: number; appKey: string }) {
  const { api } = useApp();
  const state = useAsync(
    () => api.auditEvents({ appKey, gitSourceID: sourceID, limit: 250 }),
    [api, sourceID, appKey],
  );

  return (
    <Panel title="Audit trail" subtitle="Repository, release, and input-setting changes for this app.">
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading /> : null}
      {state.data ? <AuditEventTable events={state.data} emptyTitle="No changes have been recorded for this app." /> : null}
    </Panel>
  );
}
