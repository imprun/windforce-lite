import { useEffect, useState } from "react";
import { Layout } from "../components/Layout";
import {
  DefinitionList,
  EmptyState,
  ErrorNotice,
  Field,
  JsonBlock,
  Loading,
  Panel,
  ProbeNotice,
  ReleaseStateBadge,
} from "../components/ui";
import { PublishReleaseDialog } from "../features/PublishReleaseDialog";
import {
  errorMessage,
  type ActionView,
  type AppDetail,
  type AppSummary,
  type GitSource,
  type ProbeResult,
} from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatJSON, formatRelative, formatTime, shortSHA } from "../lib/format";
import { forgeCommitURL, forgeName, forgeTreeURL } from "../lib/repo";
import { Link, useRouter } from "../lib/router";

const tabs = [
  { key: "overview", label: "Overview" },
  { key: "repository", label: "Repository" },
  { key: "releases", label: "Releases" },
  { key: "actions", label: "Actions" },
] as const;

type TabKey = (typeof tabs)[number]["key"];

export function AppDetailPage({ sourceID, tab }: { sourceID: number; tab: string }) {
  const { api } = useApp();
  const { navigate } = useRouter();
  const [publishing, setPublishing] = useState(false);

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

  return (
    <Layout
      title={source ? source.name : app!.app_key}
      subtitle={
        source
          ? `Repository source #${source.id} · ${source.repo_url}`
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

      {activeTab === "overview" ? <OverviewTab source={source} app={app} detail={detail} onPublish={() => setPublishing(true)} /> : null}
      {activeTab === "repository" && source ? <RepositoryTab source={source} onChanged={state.reload} /> : null}
      {activeTab === "releases" ? (
        <ReleasesTab appKey={app ? app.app_key : source!.name} released={Boolean(app)} repoURL={source?.repo_url || ""} />
      ) : null}
      {activeTab === "actions" ? <ActionsTab app={app} detail={detail} /> : null}

      {publishing && source ? (
        <PublishReleaseDialog
          source={source}
          onClose={() => setPublishing(false)}
          onPublished={() => {
            setPublishing(false);
            state.reload();
            navigate(`/apps/${source.id}/releases`);
          }}
        />
      ) : null}
    </Layout>
  );
}

function OverviewTab({
  source,
  app,
  detail,
  onPublish,
}: {
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
      <Panel title="Active contract" subtitle="What workers read when they execute this app.">
        <DefinitionList
          items={[
            ["App key", <span className="mono">{app.app_key}</span>],
            ["Release commit", <CommitRef repoURL={source?.repo_url || ""} commit={app.commit_sha} />],
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
            ["Timeout", `${app.timeout_s}s`],
            ["Required capabilities", app.required_capabilities?.length ? app.required_capabilities.join(", ") : "none"],
            ["Updated", `${formatTime(app.updated_at)} (${formatRelative(app.updated_at)})`],
          ]}
        />
      </Panel>

      <Panel title="Actions" subtitle={`${detail.actions.length} action(s) exposed by the active contract.`}>
        <div className="tableWrap">
          <table className="table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Route tag</th>
                <th>Timeout</th>
                <th>Capabilities</th>
              </tr>
            </thead>
            <tbody>
              {detail.actions.map((action) => (
                <tr key={action.action_key}>
                  <td>
                    <span className="cellTitle mono">{action.action_key}</span>
                  </td>
                  <td className="mono">{action.effective_route_tag || routeTag}</td>
                  <td>{action.timeout_s ? `${action.timeout_s}s` : `${app.timeout_s}s (app default)`}</td>
                  <td>{action.effective_capabilities?.length ? action.effective_capabilities.join(", ") : "none"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel title="Readiness" subtitle="Signals to check before relying on this contract.">
        <DefinitionList
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

function RepositoryTab({ source, onChanged }: { source: GitSource; onChanged: () => void }) {
  const { api, notify } = useApp();
  const { navigate } = useRouter();
  const [name, setName] = useState(source.name);
  const [repoURL, setRepoURL] = useState(source.repo_url);
  const [branch, setBranch] = useState(source.branch || "main");
  const [subpath, setSubpath] = useState(source.subpath);
  const [credsRef, setCredsRef] = useState(source.creds_ref);
  const [busy, setBusy] = useState(false);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    setName(source.name);
    setRepoURL(source.repo_url);
    setBranch(source.branch || "main");
    setSubpath(source.subpath);
    setCredsRef(source.creds_ref);
  }, [source]);

  const dirty =
    name !== source.name ||
    repoURL !== source.repo_url ||
    branch !== (source.branch || "main") ||
    subpath !== source.subpath ||
    credsRef !== source.creds_ref;

  async function handleSave() {
    setBusy(true);
    setError("");
    try {
      await api.patchGitSource(source.id, {
        ...(name !== source.name ? { name } : {}),
        ...(repoURL !== source.repo_url ? { repo_url: repoURL } : {}),
        ...(branch !== (source.branch || "main") ? { branch } : {}),
        ...(subpath !== source.subpath ? { subpath } : {}),
        ...(credsRef !== source.creds_ref ? { creds_ref: credsRef } : {}),
      });
      notify("ok", "Repository settings saved.");
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function handleProbe() {
    setBusy(true);
    setError("");
    setProbe(null);
    try {
      setProbe(await api.probeGitSource({ repo_url: repoURL, branch, creds_ref: credsRef || undefined }));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete() {
    const confirmed = window.confirm(
      `Remove app ${source.name}? The repository source registration is deleted; release history remains in the audit trail.`,
    );
    if (!confirmed) return;
    setBusy(true);
    try {
      await api.deleteGitSource(source.id);
      notify("ok", `Removed ${source.name}.`);
      navigate("/");
    } catch (cause) {
      notify("error", errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <>
      <Panel
        title="Repository settings"
        subtitle="Where releases for this app come from. Changes are re-validated against the remote."
        actions={
          <>
            <button className="button" type="button" disabled={busy} onClick={handleProbe}>
              Probe repository
            </button>
            <button className="button primary" type="button" disabled={busy || !dirty} onClick={handleSave}>
              Save changes
            </button>
          </>
        }
      >
        <div className="formGrid">
          <Field label="App name">
            <input value={name} onChange={(event) => setName(event.target.value)} />
          </Field>
          <Field label="Repository URL">
            <input value={repoURL} onChange={(event) => setRepoURL(event.target.value)} />
          </Field>
          <Field label="Branch">
            <input value={branch} onChange={(event) => setBranch(event.target.value)} />
          </Field>
          <Field label="Subpath">
            <input value={subpath} onChange={(event) => setSubpath(event.target.value)} placeholder="(repo root)" />
          </Field>
          <Field label="Creds ref" hint="Workspace variable path holding the git credential.">
            <input value={credsRef} onChange={(event) => setCredsRef(event.target.value)} placeholder="(public repository)" />
          </Field>
        </div>
        {probe ? <ProbeNotice probe={probe} branch={branch} /> : null}
        {error ? <div className="inlineNotice error">{error}</div> : null}
        <DefinitionList
          items={[
            ["Kind", source.kind],
            ["Registered", formatTime(source.created_at)],
            ["Last release commit", <span className="mono">{shortSHA(source.last_synced_commit, 16)}</span>],
          ]}
        />
      </Panel>

      <Panel title="Danger zone" subtitle="Destructive operations for this app.">
        <div className="dangerRow">
          <div>
            <p className="cellTitle">Remove app</p>
            <p className="cellSub">
              Deletes the repository source registration. Release history stays in the audit trail; the active contract
              is removed from the catalog.
            </p>
          </div>
          <button className="button danger" type="button" disabled={busy} onClick={handleDelete}>
            Remove app
          </button>
        </div>
      </Panel>
    </>
  );
}

function ReleasesTab({ appKey, released, repoURL }: { appKey: string; released: boolean; repoURL: string }) {
  const { api } = useApp();
  const state = useAsync(async () => (released ? api.appHistory(appKey) : Promise.resolve([])), [api, appKey, released]);

  return (
    <Panel title="Release history" subtitle="Who published which commit, and why.">
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
                <th>Release id</th>
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
                  <td className="mono">{item.deployment_id ? shortSHA(item.deployment_id, 12) : "—"}</td>
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

function ActionsTab({ app, detail }: { app: AppSummary | null; detail: AppDetail | null }) {
  if (!app || !detail || detail.actions.length === 0) {
    return (
      <Panel title="Actions" subtitle="Materialized action schemas from the active contract.">
        <EmptyState title="No released actions." >
          <p>Publish a release first; action schemas come from the materialized contract.</p>
        </EmptyState>
      </Panel>
    );
  }
  return (
    <>
      {detail.actions.map((action) => (
        <ActionPanel key={action.action_key} appKey={app.app_key} action={action} />
      ))}
    </>
  );
}

function ActionPanel({ appKey, action }: { appKey: string; action: ActionView }) {
  const { api } = useApp();
  const schemas = useAsync(() => api.actionSchemas(appKey, action.action_key), [api, appKey, action.action_key]);

  return (
    <Panel
      title={`Action · ${action.action_key}`}
      subtitle="Materialized JSON Schemas from the active contract. Invoke actions through the control-plane API or CLI."
    >
      {schemas.error ? <ErrorNotice message={schemas.error} onRetry={schemas.reload} /> : null}
      <div className="schemaGrid">
        <div>
          <h3 className="subHeading">Input schema</h3>
          <JsonBlock value={schemas.data ? formatJSON(schemas.data.input_schema) : "loading…"} maxHeight={280} />
        </div>
        <div>
          <h3 className="subHeading">Output schema</h3>
          <JsonBlock value={schemas.data ? formatJSON(schemas.data.output_schema) : "loading…"} maxHeight={280} />
        </div>
      </div>
    </Panel>
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
