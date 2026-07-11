"use client";

import type { ReactNode } from "react";
import type { AppDetail, AppHistoryItem, AppSummary } from "@/entities/app";
import type { GitSource } from "@/entities/git-source";
import type { DetailPage, DetailTab } from "./types";
import { formatDate, shortID } from "@/shared/lib/format";

export type CommonProps = {
  sources: GitSource[];
  apps: AppSummary[];
  selectedSourceID: number | null;
  selectedSource: GitSource | null;
  selectedApp: AppSummary | null;
  detail: AppDetail | null;
  history: AppHistoryItem[];
  sourceFiles: Record<string, string>;
  search: string;
  activeTab: DetailTab;
  actor: string;
  liveWorkers: number;
  credentialCount: number;
  detailPage: DetailPage | null;
  onSearch: (value: string) => void;
  onSelectSource: (id: number) => void;
  onRegister: () => void;
  onDeploySource: (source: GitSource) => void;
  onOpenSourceDetail: (sourceID: number) => void;
  onBackToList: () => void;
  onRemove: (source: GitSource) => void;
  onTabChange: (tab: DetailTab) => void;
  onSettings: () => void;
};

export function AppsSection(props: CommonProps) {
  return (
    <div id="deploymentOverview" className="deploymentConsole">
      <section className="deploymentCommandBar">
        <div className="commandSummary">
          <MetricTile label="Registered apps" value={String(props.sources.length)} />
          <MetricTile label="Active contracts" value={String(props.apps.length)} tone={props.apps.length ? "ok" : "neutral"} />
          <MetricTile label="Git credentials" value={String(props.credentialCount)} />
          <MetricTile label="Live workers" value={String(props.liveWorkers)} tone={props.liveWorkers ? "ok" : "warn"} />
        </div>
        <div className="commandActions">
          <button className="button primary" type="button" onClick={props.onRegister}>Register App</button>
          <button className="button" type="button" onClick={props.onSettings}>Settings</button>
        </div>
      </section>

      <section id="appList" className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Apps"
          title="Registered apps"
          description="Each app points at one repository source. Publish a release to make its contract visible to workers."
        />
        <AppToolbar search={props.search} onSearch={props.onSearch} />
        <RegisteredAppTable {...props} />
      </section>

      <section className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Active contracts"
          title="Worker-visible apps"
          description="These contracts are what workers read when jobs arrive for an app/action."
        />
        <AppTable apps={props.apps} selectedApp={props.selectedApp} onSelectSource={props.onSelectSource} sources={props.sources} />
      </section>
    </div>
  );
}

export function ReleasesSection(props: CommonProps) {
  return (
    <div id="deploymentOverview" className="workspaceGrid releasesGrid">
      <section className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Active contracts"
          title="Worker-visible apps"
          description="These contracts are generated from the latest published release."
        />
        <AppTable apps={props.apps} selectedApp={props.selectedApp} onSelectSource={props.onSelectSource} sources={props.sources} />
      </section>
      <ContractDetail {...props} />
    </div>
  );
}

export function AuditSection(props: CommonProps) {
  return (
    <div id="deploymentOverview" className="workspaceGrid auditGrid">
      <section id="deploymentHistory" className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Release history"
          title="Audit trail"
          description="Audit entries show the published commit, actor, release id, and note."
        />
        <div className="auditTable">
          {props.history.length === 0 ? <EmptyLine>No release history for the selected app.</EmptyLine> : null}
          {props.history.map((item) => (
            <div className="auditRow" key={item.id}>
              <div>
                <strong>{item.source || "deploy"}</strong>
                <span>{item.deployment_id ? shortID(item.deployment_id, 12) : "sync"}</span>
              </div>
              <div>
                <strong>{shortID(item.commit_sha, 16)}</strong>
                <span>{item.entrypoint || "-"}</span>
              </div>
              <div>
                <strong>{item.created_by || "-"}</strong>
                <span>{formatDate(item.created_at)}</span>
              </div>
              <p>{item.message || "-"}</p>
            </div>
          ))}
        </div>
      </section>
      <SourceSnapshotPanel files={props.sourceFiles} />
    </div>
  );
}

function AppToolbar({ search, onSearch }: { search: string; onSearch: (value: string) => void }) {
  return (
    <div className="tableToolbar">
      <label className="field searchField">
        Search
        <input id="sourceSearch" value={search} onChange={(event) => onSearch(event.target.value)} placeholder="app, repository, branch, subpath" spellCheck={false} />
      </label>
      <div className="toolbarHint">Open an app sheet to inspect repository settings, active contract, release history, and readiness.</div>
    </div>
  );
}

function RegisteredAppTable(props: CommonProps) {
  const filtered = props.sources.filter((source) => {
    const needle = props.search.trim().toLowerCase();
    if (!needle) return true;
    return [source.name, source.repo_url, source.branch, source.subpath].some((value) => String(value || "").toLowerCase().includes(needle));
  });

  return (
    <div className="dataTable sourceTable deployment">
      <div className="tableHead">
        <span>App</span>
        <span>Repository</span>
        <span>Repository path</span>
        <span>Current release</span>
        <span>Last release</span>
        <span>Action</span>
      </div>
      {filtered.length === 0 ? <EmptyLine>No app matches this filter.</EmptyLine> : null}
      {filtered.map((source) => {
        const app = props.apps.find((item) => item.git_source_id === source.id) || null;
        const selected = source.id === props.selectedSourceID;
        return (
          <div className={`tableRow ${selected ? "selected" : ""}`} key={source.id}>
            <button className="tableCellButton sourceIdentity" type="button" aria-label={`Open app ${source.name}`} onClick={() => props.onOpenSourceDetail(source.id)}>
              <span className={source.last_synced_commit ? "statusDot ok" : "statusDot warn"} aria-hidden="true" />
              <span>
                <strong>{source.name}</strong>
                <small>{source.last_synced_commit ? "released" : "registered"}</small>
              </span>
            </button>
            <div className="tableCell repoCell">{source.repo_url}</div>
            <div className="tableCell">
              <strong>{source.branch || "main"}</strong>
              <small>{source.subpath || "root"}</small>
            </div>
            <div className="tableCell">
              <strong>{app?.app_key || "-"}</strong>
              <small>{app ? `${app.entrypoint || "-"} / ${shortID(app.commit_sha, 10)}` : "not released"}</small>
            </div>
            <div className="tableCell">
              <strong>{formatCompactDate(source.last_synced_at)}</strong>
              <small>{shortID(source.last_synced_commit, 12)}</small>
            </div>
            <div className="rowButtons nowrap">
              <button className="button compactButton" type="button" onClick={() => props.onOpenSourceDetail(source.id)}>Open</button>
              <button className="button primary compactButton" type="button" onClick={() => props.onDeploySource(source)}>Publish</button>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function AppTable({ apps, sources, selectedApp, onSelectSource }: { apps: AppSummary[]; sources: GitSource[]; selectedApp: AppSummary | null; onSelectSource: (id: number) => void }) {
  return (
    <div className="dataTable appTable">
      <div className="tableHead">
        <span>App</span>
        <span>Entrypoint</span>
        <span>Commit</span>
        <span>Actions</span>
        <span>Updated</span>
      </div>
      {apps.length === 0 ? <EmptyLine>No active contracts.</EmptyLine> : null}
      {apps.map((app) => {
        const source = sources.find((item) => item.id === app.git_source_id);
        return (
          <div className={`tableRow ${selectedApp?.app_key === app.app_key ? "selected" : ""}`} key={app.app_key}>
            <button className="tableCellButton" type="button" onClick={() => source ? onSelectSource(source.id) : undefined}>
              <strong>{app.app_key}</strong>
              <small>{source?.name || `repository ${app.git_source_id}`}</small>
            </button>
            <div className="tableCell">{app.entrypoint || "-"}</div>
            <div className="tableCell">{shortID(app.commit_sha, 16)}</div>
            <div className="tableCell">{String(app.actions_count || 0)}</div>
            <div className="tableCell">{formatCompactDate(app.updated_at)}</div>
          </div>
        );
      })}
    </div>
  );
}

function ContractDetail(props: CommonProps) {
  const app = props.selectedApp;
  return (
    <section id="appDetail" className="workspacePanel releaseBrief">
      <PanelHeader eyebrow="Selected contract" title={app?.app_key || "No contract selected"} description={app?.entrypoint || "Select an active app contract."} />
      <div className="tabBar" role="tablist" aria-label="Release contract tabs">
        <TabButton id="tab-contract" active={props.activeTab === "contract"} onClick={() => props.onTabChange("contract")}>Contract</TabButton>
        <TabButton id="tab-history" active={props.activeTab === "history"} onClick={() => props.onTabChange("history")}>History</TabButton>
        <TabButton id="tab-source" active={props.activeTab === "source"} onClick={() => props.onTabChange("source")}>Repository Snapshot</TabButton>
      </div>
      {props.activeTab === "contract" ? <ContractTab app={app} detail={props.detail} /> : null}
      {props.activeTab === "history" ? <HistoryTab history={props.history} /> : null}
      {props.activeTab === "source" ? <SourceSnapshotPanel files={props.sourceFiles} compact /> : null}
    </section>
  );
}

export function ContractTab({ app, detail }: { app: AppSummary | null; detail: AppDetail | null }) {
  if (!app) return <div id="actionList" className="emptyState"><strong>No active contract</strong><p>Publish an app release first.</p></div>;
  return (
    <div className="contractTab">
      <div className="sourceDetailGrid">
        <Field label="App key" value={app.app_key} />
        <Field label="Entrypoint" value={app.entrypoint || "-"} />
        <Field label="Commit" value={shortID(app.commit_sha, 16)} />
        <Field label="Updated" value={formatDate(app.updated_at)} />
      </div>
      <div id="actionList" className="actionList">
        {(detail?.actions || []).length === 0 ? <EmptyLine>No actions exposed by this contract.</EmptyLine> : null}
        {(detail?.actions || []).map((action) => (
          <div className="actionRow" key={action.action_key}>
            <div>
              <strong>{action.action_key}</strong>
              <p>{(action.effective_capabilities || []).join(", ") || "no capabilities"}</p>
            </div>
            <span className="pill subtle">{action.effective_route_tag || "default"}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function HistoryTab({ history }: { history: AppHistoryItem[] }) {
  return (
    <div id="deploymentHistory" className="historyList">
      {history.length === 0 ? <EmptyLine>No release history.</EmptyLine> : null}
      {history.map((item) => <HistoryItem item={item} key={item.id} />)}
    </div>
  );
}

export function ReadinessPanel({ source, app, actor, liveWorkers }: { source: GitSource | null; app: AppSummary | null; actor: string; liveWorkers: number }) {
  return (
    <div className="readinessPanel">
      <span className="eyebrow">Readiness</span>
      <div className="readinessList">
        <ReadinessItem ok={Boolean(source)} label="App registered" detail={source?.name || "No app selected"} />
        <ReadinessItem ok={Boolean(source?.repo_url)} label="Repository URL" detail={source?.repo_url || "Missing"} />
        <ReadinessItem ok={Boolean(app)} label="Active contract" detail={app ? `${app.app_key} / ${shortID(app.commit_sha, 10)}` : "First release required"} warnOnly />
        <ReadinessItem ok={Boolean(actor.trim())} label="Audit actor" detail={actor.trim() || "Set in Settings before publishing"} />
        <ReadinessItem ok={liveWorkers > 0} label="Live workers" detail={`${liveWorkers} worker${liveWorkers === 1 ? "" : "s"}`} warnOnly />
      </div>
    </div>
  );
}

export function LatestAudit({ history, title = "Release history" }: { history: AppHistoryItem[]; title?: string }) {
  return (
    <div id="auditTimeline" className="latestAudit">
      <span className="eyebrow">{title}</span>
      <div className="historyList compactHistory">
        {history.length === 0 ? <EmptyLine>No audit entries for this app.</EmptyLine> : null}
        {history.slice(0, 4).map((item) => <HistoryItem item={item} key={item.id} compact />)}
      </div>
    </div>
  );
}

export function SourceSnapshotPanel({ files, compact = false }: { files: Record<string, string>; compact?: boolean }) {
  return (
    <section className={compact ? "snapshotPanel compact" : "workspacePanel snapshotPanel"}>
      {!compact ? <PanelHeader eyebrow="Repository snapshot" title="Materialized files" description="The worker contract was built from this repository snapshot." /> : null}
      <pre id="sourceSnapshot" className="codeView">
        {Object.entries(files).slice(0, 4).map(([name, content]) => `# ${name}\n${content}`).join("\n\n") || "No repository snapshot loaded."}
      </pre>
    </section>
  );
}

export function PanelHeader({ eyebrow, title, description, action }: { eyebrow: string; title: string; description?: string; action?: ReactNode }) {
  return (
    <header className="panelHeader">
      <div>
        <span className="eyebrow">{eyebrow}</span>
        <h2>{title}</h2>
        {description ? <p>{description}</p> : null}
      </div>
      {action ? <div className="panelAction">{action}</div> : null}
    </header>
  );
}

function MetricTile({ label, value, tone = "neutral" }: { label: string; value: string; tone?: "neutral" | "ok" | "warn" }) {
  return (
    <div className={`metricTile ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function TabButton({ id, active, children, onClick }: { id: string; active: boolean; children: ReactNode; onClick: () => void }) {
  return (
    <button id={id} className={`tabButton ${active ? "active" : ""}`} type="button" role="tab" aria-selected={active} onClick={onClick}>
      {children}
    </button>
  );
}

export function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="kv">
      <span>{label}</span>
      <strong>{value || "-"}</strong>
    </div>
  );
}

function ReadinessItem({ ok, label, detail, warnOnly = false }: { ok: boolean; label: string; detail: string; warnOnly?: boolean }) {
  return (
    <div className="readinessItem">
      <span className={ok ? "statusDot ok" : warnOnly ? "statusDot warn" : "statusDot error"} aria-hidden="true" />
      <div>
        <strong>{label}</strong>
        <p>{detail}</p>
      </div>
    </div>
  );
}

function HistoryItem({ item, compact = false }: { item: AppHistoryItem; compact?: boolean }) {
  return (
    <article className={compact ? "historyItem compact" : "historyItem"}>
      <strong>{item.source || "release"} / {shortID(item.commit_sha, 12)}</strong>
      <p>{item.created_by || "-"} / {formatDate(item.created_at)}</p>
      {!compact ? <p>{item.deployment_id ? `release ${shortID(item.deployment_id, 10)}` : "sync"}{item.message ? ` / ${item.message}` : ""}</p> : null}
    </article>
  );
}

export function EmptyLine({ children }: { children: ReactNode }) {
  return <p className="emptyText">{children}</p>;
}

function formatCompactDate(value: unknown): string {
  if (!value) return "-";
  const date = new Date(String(value));
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}
