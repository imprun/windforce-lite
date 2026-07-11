"use client";

import type { ReactNode } from "react";
import type { AppDetail, AppHistoryItem, AppSummary } from "@/entities/app";
import type { GitSource } from "@/entities/git-source";
import type { DetailTab } from "./types";
import { formatDate, shortID } from "@/shared/lib/format";

type CommonProps = {
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
  onSearch: (value: string) => void;
  onSelectSource: (id: number) => void;
  onRegister: () => void;
  onDeploy: (source: GitSource) => void;
  onRemove: (source: GitSource) => void;
  onTabChange: (tab: DetailTab) => void;
  onSettings: () => void;
};

export function DeploymentsSection(props: CommonProps) {
  return (
    <div id="deploymentOverview" className="workspaceGrid">
      <section id="sourceList" className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Deployment queue"
          title="FCode release candidates"
          description="Review deployable sources, current contract state, and last published commit."
          action={<button className="button primary" type="button" aria-label="Register source from deployment queue" onClick={props.onRegister}>Register Source</button>}
        />
        <SourceToolbar search={props.search} onSearch={props.onSearch} />
        <SourceTable {...props} mode="deployment" />
      </section>
      <ReleaseBrief {...props} />
    </div>
  );
}

export function SourcesSection(props: CommonProps) {
  return (
    <div id="deploymentOverview" className="workspaceGrid">
      <section id="sourceList" className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Source registry"
          title="Registered Git sources"
          description="Each row must point at a Windforce manifest that can be materialized into a worker contract."
          action={<button className="button primary" type="button" aria-label="Register source from source registry" onClick={props.onRegister}>Register Source</button>}
        />
        <SourceToolbar search={props.search} onSearch={props.onSearch} />
        <SourceTable {...props} mode="source" />
      </section>
      <SourceOperationsPanel {...props} />
    </div>
  );
}

export function ReleasesSection(props: CommonProps) {
  return (
    <div id="deploymentOverview" className="workspaceGrid releasesGrid">
      <section className="workspacePanel queuePanel">
        <PanelHeader
          eyebrow="Release contracts"
          title="Worker-visible apps"
          description="These contracts are what workers use when jobs arrive for an app/action."
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
          eyebrow="Deployment audit"
          title="Release activity"
          description="Audit entries show the published commit, actor, deployment id, and note."
        />
        <div className="auditTable">
          {props.history.length === 0 ? <EmptyLine>No deployment history for the selected FCode.</EmptyLine> : null}
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

function SourceToolbar({ search, onSearch }: { search: string; onSearch: (value: string) => void }) {
  return (
    <div className="tableToolbar">
      <label className="field searchField">
        Search
        <input id="sourceSearch" value={search} onChange={(event) => onSearch(event.target.value)} placeholder="FCode, repo, branch, subpath" spellCheck={false} />
      </label>
      <div className="toolbarHint">Select a row to inspect readiness and release details.</div>
    </div>
  );
}

function SourceTable(props: CommonProps & { mode: "deployment" | "source" }) {
  const filtered = props.sources.filter((source) => {
    const needle = props.search.trim().toLowerCase();
    if (!needle) return true;
    return [source.name, source.repo_url, source.branch, source.subpath].some((value) => String(value || "").toLowerCase().includes(needle));
  });

  return (
    <div className={`dataTable sourceTable ${props.mode}`}>
      <div className="tableHead">
        <span>FCode</span>
        <span>Repository</span>
        <span>Branch / path</span>
        <span>Current release</span>
        <span>Last deployed</span>
      </div>
      {filtered.length === 0 ? <EmptyLine>No source matches this filter.</EmptyLine> : null}
      {filtered.map((source) => {
        const app = props.apps.find((item) => item.git_source_id === source.id) || null;
        const selected = source.id === props.selectedSourceID;
        return (
          <div className={`tableRow ${selected ? "selected" : ""}`} key={source.id}>
            <button className="tableCellButton sourceIdentity" type="button" aria-label={`Select source ${source.name}`} onClick={() => props.onSelectSource(source.id)}>
              <span className={source.last_synced_commit ? "statusDot ok" : "statusDot warn"} aria-hidden="true" />
              <span>
                <strong>{source.name}</strong>
                <small>{source.last_synced_commit ? "deployed" : "registered"}</small>
              </span>
            </button>
            <div className="tableCell repoCell">{source.repo_url}</div>
            <div className="tableCell">
              <strong>{source.branch || "main"}</strong>
              <small>{source.subpath || "root"}</small>
            </div>
            <div className="tableCell">
              <strong>{app?.app_key || "-"}</strong>
              <small>{app ? `${app.entrypoint || "-"} / ${shortID(app.commit_sha, 10)}` : "not deployed"}</small>
            </div>
            <div className="tableCell">
              <strong>{formatCompactDate(source.last_synced_at)}</strong>
              <small>{shortID(source.last_synced_commit, 12)}</small>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function ReleaseBrief(props: CommonProps) {
  const source = props.selectedSource;
  const app = props.selectedApp;
  if (!source) {
    return (
      <section id="sourceDetail" className="workspacePanel releaseBrief emptyPanel">
        <span className="eyebrow">No selection</span>
        <h2>Register a deployable FCode source</h2>
        <p>Start by connecting a Git repository that contains a valid Windforce manifest.</p>
        <button className="button primary" type="button" aria-label="Register source from empty release brief" onClick={props.onRegister}>Register Source</button>
      </section>
    );
  }

  return (
    <section id="sourceDetail" className="workspacePanel releaseBrief">
      <header className="briefHeader">
        <div>
          <span className="eyebrow">Release brief</span>
          <h2>{source.name}</h2>
          <p>{source.repo_url}</p>
        </div>
        <span className={source.last_synced_commit ? "badge ok" : "badge warn"}>{source.last_synced_commit ? "Deployed" : "Registered"}</span>
      </header>

      <div className="briefActions">
        <button id="deploySelectedSource" className="button primary" type="button" onClick={() => props.onDeploy(source)}>Deploy selected commit</button>
        <button className="button" type="button" onClick={props.onSettings}>Set actor</button>
        <button className="button dangerGhost" type="button" onClick={() => props.onRemove(source)}>Remove</button>
      </div>

      <div className="releaseBlock">
        <span className="eyebrow">Current worker contract</span>
        <strong>{app?.app_key || "No active contract"}</strong>
        <p>{app ? `${app.entrypoint || "entrypoint not set"} / ${shortID(app.commit_sha, 14)}` : "Deploy this source to publish a worker-visible contract."}</p>
      </div>

      <div className="briefMeta">
        <Field label="Branch" value={source.branch || "main"} />
        <Field label="Subpath" value={source.subpath || "root"} />
        <Field label="Credential" value={source.creds_ref ? "configured" : "public repository"} />
        <Field label="Route tag" value={app?.effective_route_tag || app?.tag || "default"} />
        <Field label="Actions" value={String(props.detail?.actions?.length ?? app?.actions_count ?? 0)} />
        <Field label="Language" value={app?.script_lang || "-"} />
      </div>

      <ReadinessPanel source={source} app={app} actor={props.actor} liveWorkers={props.liveWorkers} />
      <LatestAudit history={props.history} />
    </section>
  );
}

function SourceOperationsPanel(props: CommonProps) {
  const source = props.selectedSource;
  if (!source) return <ReleaseBrief {...props} />;
  return (
    <section id="sourceDetail" className="workspacePanel sourceOpsPanel">
      <PanelHeader
        eyebrow="Source detail"
        title={source.name}
        description={source.repo_url}
        action={<button className="button primary" type="button" onClick={() => props.onDeploy(source)}>Deploy</button>}
      />
      <div className="sourceDetailGrid">
        <Field label="Branch" value={source.branch || "main"} />
        <Field label="Subpath" value={source.subpath || "root"} />
        <Field label="Credential" value={source.creds_ref ? "configured" : "public repository"} />
        <Field label="Last commit" value={shortID(source.last_synced_commit, 16)} />
        <Field label="Last deployed" value={formatDate(source.last_synced_at)} />
        <Field label="Kind" value={source.kind || "git"} />
      </div>
      <ReadinessPanel source={source} app={props.selectedApp} actor={props.actor} liveWorkers={props.liveWorkers} />
      <div className="dangerZone">
        <div>
          <strong>Remove source registration</strong>
          <p>Deployment history and active contracts remain in the control plane.</p>
        </div>
        <button className="button dangerGhost" type="button" onClick={() => props.onRemove(source)}>Remove Source</button>
      </div>
    </section>
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
      {apps.length === 0 ? <EmptyLine>No active app contracts.</EmptyLine> : null}
      {apps.map((app) => {
        const source = sources.find((item) => item.id === app.git_source_id);
        return (
          <div className={`tableRow ${selectedApp?.app_key === app.app_key ? "selected" : ""}`} key={app.app_key}>
            <button className="tableCellButton" type="button" onClick={() => source ? onSelectSource(source.id) : undefined}>
              <strong>{app.app_key}</strong>
              <small>{source?.name || `source ${app.git_source_id}`}</small>
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
        <TabButton id="tab-source" active={props.activeTab === "source"} onClick={() => props.onTabChange("source")}>Source Snapshot</TabButton>
      </div>
      {props.activeTab === "contract" ? <ContractTab app={app} detail={props.detail} /> : null}
      {props.activeTab === "history" ? <HistoryTab history={props.history} /> : null}
      {props.activeTab === "source" ? <SourceSnapshotPanel files={props.sourceFiles} compact /> : null}
    </section>
  );
}

function ContractTab({ app, detail }: { app: AppSummary | null; detail: AppDetail | null }) {
  if (!app) return <div id="actionList" className="emptyState"><strong>No deployed contract</strong><p>Select or deploy a source first.</p></div>;
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
      {history.length === 0 ? <EmptyLine>No deployment history.</EmptyLine> : null}
      {history.map((item) => <HistoryItem item={item} key={item.id} />)}
    </div>
  );
}

function ReadinessPanel({ source, app, actor, liveWorkers }: { source: GitSource | null; app: AppSummary | null; actor: string; liveWorkers: number }) {
  return (
    <div className="readinessPanel">
      <span className="eyebrow">Readiness</span>
      <div className="readinessList">
        <ReadinessItem ok={Boolean(source)} label="Source registered" detail={source?.name || "No source selected"} />
        <ReadinessItem ok={Boolean(source?.repo_url)} label="Repository URL" detail={source?.repo_url || "Missing"} />
        <ReadinessItem ok={Boolean(app)} label="Active contract" detail={app ? `${app.app_key} / ${shortID(app.commit_sha, 10)}` : "First deployment required"} warnOnly />
        <ReadinessItem ok={Boolean(actor.trim())} label="Audit actor" detail={actor.trim() || "Set in Settings before deploy"} />
        <ReadinessItem ok={liveWorkers > 0} label="Live workers" detail={`${liveWorkers} worker${liveWorkers === 1 ? "" : "s"}`} warnOnly />
      </div>
    </div>
  );
}

function LatestAudit({ history }: { history: AppHistoryItem[] }) {
  return (
    <div id="auditTimeline" className="latestAudit">
      <span className="eyebrow">Latest audit</span>
      <div className="historyList compactHistory">
        {history.length === 0 ? <EmptyLine>No audit entries for this FCode.</EmptyLine> : null}
        {history.slice(0, 4).map((item) => <HistoryItem item={item} key={item.id} compact />)}
      </div>
    </div>
  );
}

function SourceSnapshotPanel({ files, compact = false }: { files: Record<string, string>; compact?: boolean }) {
  return (
    <section className={compact ? "snapshotPanel compact" : "workspacePanel snapshotPanel"}>
      {!compact ? <PanelHeader eyebrow="Source snapshot" title="Materialized files" description="The worker contract was built from this source snapshot." /> : null}
      <pre id="sourceSnapshot" className="codeView">
        {Object.entries(files).slice(0, 4).map(([name, content]) => `# ${name}\n${content}`).join("\n\n") || "No source snapshot loaded."}
      </pre>
    </section>
  );
}

function PanelHeader({ eyebrow, title, description, action }: { eyebrow: string; title: string; description?: string; action?: ReactNode }) {
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

function TabButton({ id, active, children, onClick }: { id: string; active: boolean; children: ReactNode; onClick: () => void }) {
  return (
    <button id={id} className={`tabButton ${active ? "active" : ""}`} type="button" role="tab" aria-selected={active} onClick={onClick}>
      {children}
    </button>
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
      <strong>{item.source || "deploy"} / {shortID(item.commit_sha, 12)}</strong>
      <p>{item.created_by || "-"} / {formatDate(item.created_at)}</p>
      {!compact ? <p>{item.deployment_id ? `deploy ${shortID(item.deployment_id, 10)}` : "sync"}{item.message ? ` / ${item.message}` : ""}</p> : null}
    </article>
  );
}

function EmptyLine({ children }: { children: ReactNode }) {
  return <p className="emptyText">{children}</p>;
}

function formatCompactDate(value: unknown): string {
  if (!value) return "-";
  const date = new Date(String(value));
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}
