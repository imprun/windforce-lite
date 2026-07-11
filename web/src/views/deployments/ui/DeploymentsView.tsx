"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import type { AppDetail, AppHistoryItem, AppSummary } from "@/entities/app";
import type { GitSource } from "@/entities/git-source";
import { DeploySourceDialog } from "@/features/deploy-source/ui/DeploySourceDialog";
import { SourceRegistrationForm } from "@/features/source-registration/ui/SourceRegistrationForm";
import { Topbar } from "@/widgets/topbar";
import { WindforceApi } from "@/shared/api/client";
import type { ApiSettings, VariableRow, WorkerTagsResponse } from "@/shared/api/types";
import { formatDate, shortID } from "@/shared/lib/format";

type Notice = {
  tone: "info" | "ok" | "error";
  text: string;
};

const defaultSettings: ApiSettings = {
  workspace: "default",
  token: "",
  actor: "",
};

export function DeploymentsView() {
  const [settings, setSettings] = useState<ApiSettings>(defaultSettings);
  const [sources, setSources] = useState<GitSource[]>([]);
  const [apps, setApps] = useState<AppSummary[]>([]);
  const [variables, setVariables] = useState<VariableRow[]>([]);
  const [workerTags, setWorkerTags] = useState<WorkerTagsResponse>({});
  const [selectedSourceID, setSelectedSourceID] = useState<number | null>(null);
  const [selectedAppKey, setSelectedAppKey] = useState("");
  const [appDetail, setAppDetail] = useState<AppDetail | null>(null);
  const [history, setHistory] = useState<AppHistoryItem[]>([]);
  const [sourceFiles, setSourceFiles] = useState<Record<string, string>>({});
  const [deploySource, setDeploySource] = useState<GitSource | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<Notice>({ tone: "info", text: "" });
  const [deployError, setDeployError] = useState("");

  useEffect(() => {
    const saved = globalThis.localStorage;
    setSettings({
      workspace: saved.getItem("wf.workspace") || "default",
      token: saved.getItem("wf.token") || "",
      actor: saved.getItem("wf.actor") || "",
    });
  }, []);

  useEffect(() => {
    localStorage.setItem("wf.workspace", settings.workspace);
    localStorage.setItem("wf.token", settings.token);
    localStorage.setItem("wf.actor", settings.actor);
  }, [settings]);

  const api = useMemo(() => new WindforceApi(settings), [settings]);
  const selectedSource = sources.find((source) => source.id === selectedSourceID) || null;
  const selectedApp = apps.find((app) => app.app_key === selectedAppKey) || null;

  const refresh = useCallback(async () => {
    setBusy(true);
    setNotice({ tone: "info", text: "Refreshing..." });
    try {
      const [nextVariables, nextSources, nextApps, nextTags] = await Promise.all([
        api.variables(),
        api.gitSources(),
        api.apps(),
        api.workerTags(),
      ]);
      setVariables(nextVariables);
      setSources(nextSources);
      setApps(nextApps.apps || []);
      setWorkerTags(nextTags);
      setSelectedSourceID((current) => {
        if (current && nextSources.some((source) => source.id === current)) return current;
        return nextSources[0]?.id || null;
      });
      setSelectedAppKey((current) => {
        if (current && nextApps.apps?.some((app) => app.app_key === current)) return current;
        return nextApps.apps?.[0]?.app_key || "";
      });
      setNotice({ tone: "ok", text: "Refreshed" });
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : String(error) });
    } finally {
      setBusy(false);
    }
  }, [api]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!selectedAppKey) {
      setAppDetail(null);
      setHistory([]);
      setSourceFiles({});
      return;
    }
    let canceled = false;
    async function loadApp() {
      try {
        const [detail, nextHistory, source] = await Promise.all([
          api.app(selectedAppKey),
          api.appHistory(selectedAppKey),
          api.appSource(selectedAppKey).catch(() => ({ files: {} })),
        ]);
        if (canceled) return;
        setAppDetail(detail);
        setHistory(nextHistory);
        setSourceFiles(source.files || {});
      } catch (error) {
        if (!canceled) setNotice({ tone: "error", text: error instanceof Error ? error.message : String(error) });
      }
    }
    void loadApp();
    return () => {
      canceled = true;
    };
  }, [api, selectedAppKey]);

  async function run(label: string, action: () => Promise<string | void>) {
    setBusy(true);
    setNotice({ tone: "info", text: label });
    try {
      const success = await action();
      setNotice({ tone: "ok", text: success || "Done" });
    } catch (error) {
      setNotice({ tone: "error", text: error instanceof Error ? error.message : String(error) });
    } finally {
      setBusy(false);
    }
  }

  async function deploy(message: string) {
    if (!deploySource) return;
    setDeployError("");
    await run(`Deploying ${deploySource.name}`, async () => {
      const result = await api.deployGitSource(deploySource.id, message ? { confirm: true, message } : { confirm: true });
      setDeploySource(null);
      setSelectedSourceID(deploySource.id);
      setSelectedAppKey(result.app);
      await refresh();
      return `Deployed ${result.app} at ${shortID(result.commit, 12)}`;
    });
  }

  return (
    <main className="appShell">
      <Topbar settings={settings} onChange={setSettings} onRefresh={refresh} busy={busy} />
      <section className="content">
        {notice.text ? <div id="notice" className={`notice ${notice.tone}`}>{notice.text}</div> : null}
        <Overview sources={sources} apps={apps} variables={variables} workerTags={workerTags} />
        <SourceRegistrationForm
          busy={busy}
          onRegister={(payload) => run("Registering source", async () => {
            const created = await api.registerGitSource(payload);
            setSelectedSourceID(created.id);
            await refresh();
          })}
          onProbe={(payload) => run("Probing repository", async () => {
            const result = await api.probeGitSource(payload);
            setNotice({
              tone: result.reachable ? "ok" : "error",
              text: result.reachable ? "Repository reachable for deployment." : result.error || "Repository is not reachable.",
            });
          })}
          onCreateSample={() => run("Creating sample", async () => {
            await api.sample("echo");
            await refresh();
          })}
        />
        <section className="managementGrid">
          <SourceList
            sources={sources}
            selectedSourceID={selectedSourceID}
            onSelect={setSelectedSourceID}
            onDeploy={setDeploySource}
            onRemove={(source) => run(`Removing ${source.name}`, async () => {
              await api.deleteGitSource(source.id);
              await refresh();
            })}
          />
          <SourceDetail source={selectedSource} app={apps.find((item) => item.git_source_id === selectedSource?.id) || null} onDeploy={setDeploySource} onOpenApp={setSelectedAppKey} />
        </section>
        <section className="managementGrid contracts">
          <AppList apps={apps} selectedAppKey={selectedAppKey} onSelect={setSelectedAppKey} />
          <AppDetailPanel
            app={selectedApp}
            detail={appDetail}
            history={history}
            sourceFiles={sourceFiles}
            source={sources.find((item) => item.id === selectedApp?.git_source_id) || null}
            onDeploy={setDeploySource}
          />
        </section>
      </section>
      <DeploySourceDialog
        source={deploySource}
        actor={settings.actor}
        busy={busy}
        error={deployError}
        onClose={() => {
          setDeployError("");
          setDeploySource(null);
        }}
        onDeploy={deploy}
      />
    </main>
  );
}

function Overview({ sources, apps, variables, workerTags }: { sources: GitSource[]; apps: AppSummary[]; variables: VariableRow[]; workerTags: WorkerTagsResponse }) {
  const liveWorkers = (workerTags.tags || []).reduce((total, tag) => total + Number(tag.live_workers || 0), 0);
  return (
    <section id="deploymentOverview" className="metricGrid">
      <Metric label="Registered sources" value={sources.length} />
      <Metric label="Active apps" value={apps.length} />
      <Metric label="Stored credentials" value={variables.filter((item) => item.path?.startsWith("git/")).length} />
      <Metric label="Live workers" value={liveWorkers} />
    </section>
  );
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function SourceList({ sources, selectedSourceID, onSelect, onDeploy, onRemove }: {
  sources: GitSource[];
  selectedSourceID: number | null;
  onSelect: (id: number) => void;
  onDeploy: (source: GitSource) => void;
  onRemove: (source: GitSource) => void;
}) {
  return (
    <section id="sourceList" className="surface">
      <header className="sectionHead">
        <div>
          <h2>Registered FCodes</h2>
          <p>Each source is a deployable Windforce manifest.</p>
        </div>
      </header>
      <div className="list">
        {sources.length === 0 ? <p className="muted">No registered FCode source.</p> : null}
        {sources.map((source) => (
          <button
            className={`listRow buttonReset ${source.id === selectedSourceID ? "selected" : ""}`}
            key={source.id}
            type="button"
            onClick={() => onSelect(source.id)}
          >
            <div>
              <strong>{source.name}</strong>
              <p>{source.repo_url}</p>
            </div>
            <span className="pill">{source.branch || "main"}</span>
            <div className="rowActions">
              <button className="button primary" type="button" onClick={(event) => {
                event.stopPropagation();
                onDeploy(source);
              }}>
                Deploy
              </button>
              <button className="button danger" type="button" onClick={(event) => {
                event.stopPropagation();
                onRemove(source);
              }}>
                Remove
              </button>
            </div>
          </button>
        ))}
      </div>
    </section>
  );
}

function SourceDetail({ source, app, onDeploy, onOpenApp }: { source: GitSource | null; app: AppSummary | null; onDeploy: (source: GitSource) => void; onOpenApp: (appKey: string) => void }) {
  if (!source) return <section className="surface empty">Select a registered FCode.</section>;
  return (
    <section id="sourceDetail" className="surface">
      <header className="sectionHead">
        <div>
          <h2>{source.name}</h2>
          <p>{source.repo_url}</p>
        </div>
        <span className={source.last_synced_commit ? "pill ok" : "pill warn"}>{source.last_synced_commit ? "deployed" : "registered"}</span>
      </header>
      <div className="detailGrid">
        <Field label="Branch" value={source.branch || "main"} />
        <Field label="Subpath" value={source.subpath || "root"} />
        <Field label="Credential" value={source.creds_ref ? "configured" : "public repository"} />
        <Field label="Last commit" value={shortID(source.last_synced_commit, 16)} />
        <Field label="Last deployed" value={formatDate(source.last_synced_at)} />
        <Field label="Active app" value={app?.app_key || "-"} />
      </div>
      <div className="actions">
        <button className="button primary" type="button" onClick={() => onDeploy(source)}>
          Deploy
        </button>
        {app ? (
          <button className="button" type="button" onClick={() => onOpenApp(app.app_key)}>
            Open Contract
          </button>
        ) : null}
      </div>
    </section>
  );
}

function AppList({ apps, selectedAppKey, onSelect }: { apps: AppSummary[]; selectedAppKey: string; onSelect: (appKey: string) => void }) {
  return (
    <section id="appList" className="surface">
      <header className="sectionHead">
        <div>
          <h2>Active Contracts</h2>
          <p>Deployment output currently visible to workers.</p>
        </div>
      </header>
      <div className="list">
        {apps.length === 0 ? <p className="muted">No active app contract.</p> : null}
        {apps.map((app) => (
          <button className={`listRow buttonReset ${app.app_key === selectedAppKey ? "selected" : ""}`} key={app.app_key} type="button" onClick={() => onSelect(app.app_key)}>
            <div>
              <strong>{app.app_key}</strong>
              <p>{app.script_lang || "unknown"} / {shortID(app.commit_sha, 12)}</p>
            </div>
            <span className="pill">{app.effective_route_tag || app.tag || "default"}</span>
          </button>
        ))}
      </div>
    </section>
  );
}

function AppDetailPanel({ app, detail, history, sourceFiles, source, onDeploy }: {
  app: AppSummary | null;
  detail: AppDetail | null;
  history: AppHistoryItem[];
  sourceFiles: Record<string, string>;
  source: GitSource | null;
  onDeploy: (source: GitSource) => void;
}) {
  if (!app) return <section className="surface empty">Select an app contract.</section>;
  return (
    <section id="appDetail" className="surface">
      <header className="sectionHead">
        <div>
          <h2>{app.app_key}</h2>
          <p>{app.entrypoint || "entrypoint not set"}</p>
        </div>
        {source ? (
          <button className="button primary" type="button" onClick={() => onDeploy(source)}>
            Deploy Source
          </button>
        ) : null}
      </header>
      <div className="detailGrid">
        <Field label="Commit" value={shortID(app.commit_sha, 16)} />
        <Field label="Language" value={app.script_lang || "-"} />
        <Field label="Route tag" value={app.effective_route_tag || app.tag || "default"} />
        <Field label="Git source" value={String(app.git_source_id || "-")} />
        <Field label="Actions" value={String(detail?.actions?.length ?? app.actions_count ?? 0)} />
        <Field label="Updated" value={formatDate(app.updated_at)} />
      </div>
      <div id="actionList" className="actionGrid">
        {(detail?.actions || []).map((action) => (
          <div className="actionItem" key={action.action_key}>
            <strong>{action.action_key}</strong>
            <span>{action.effective_route_tag || "default"}</span>
            <p>{(action.effective_capabilities || []).join(", ") || "no capabilities"}</p>
          </div>
        ))}
      </div>
      <div className="splitPane">
        <section>
          <h3>Deployment History</h3>
          <div id="deploymentHistory" className="list">
            {history.length === 0 ? <p className="muted">No deployment history.</p> : null}
            {history.map((item) => (
              <div className="historyRow" key={item.id}>
                <strong>{item.source || "external_sync"} / {shortID(item.commit_sha, 12)}</strong>
                <p>{item.created_by || "-"} / {formatDate(item.created_at)}</p>
                <p>{item.deployment_id ? `deploy ${shortID(item.deployment_id, 10)}` : "sync"} {item.message ? `/ ${item.message}` : ""}</p>
              </div>
            ))}
          </div>
        </section>
        <section>
          <h3>Source Snapshot</h3>
          <pre id="sourceSnapshot" className="codeView">{Object.entries(sourceFiles).slice(0, 4).map(([name, content]) => `# ${name}\n${content}`).join("\n\n") || "No source snapshot loaded."}</pre>
        </section>
      </div>
    </section>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="kv">
      <span>{label}</span>
      <strong>{value || "-"}</strong>
    </div>
  );
}
