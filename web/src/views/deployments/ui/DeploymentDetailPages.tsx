"use client";

import { useState } from "react";
import type { AppDetail, AppSummary } from "@/entities/app";
import type { DetailPage } from "./types";
import { formatDate, shortID } from "@/shared/lib/format";
import {
  EmptyLine,
  Field,
  LatestAudit,
  PanelHeader,
  ReadinessPanel,
  SourceSnapshotPanel,
  type CommonProps,
} from "./DeploymentPanels";

export function SourceDetailSection(props: CommonProps & { detailPage: Extract<DetailPage, { kind: "source" }> }) {
  const source = props.sources.find((item) => item.id === props.detailPage.sourceID) || null;
  const app = source ? props.apps.find((item) => item.git_source_id === source.id) || null : null;
  const actorReady = Boolean(props.actor.trim());

  if (!source) {
    return <DetailNotFound title="App not found" onBack={props.onBackToList} />;
  }

  return (
    <div id="sourceDetailPage" className="detailPage">
      <section className="workspacePanel detailHero">
        <div className="detailHeroMain">
          <button className="button compactButton" type="button" onClick={props.onBackToList}>Back to console</button>
          <div>
            <span className="eyebrow">App detail</span>
            <h2>{source.name}</h2>
            <p>{source.repo_url}</p>
          </div>
        </div>
        <div className="detailHeroActions">
          <span className={source.last_synced_commit ? "badge ok" : "badge warn"}>{source.last_synced_commit ? "released" : "registered"}</span>
          {actorReady ? (
            <button className="button primary" type="button" onClick={() => props.onDeploySource(source)}>Publish Release</button>
          ) : (
            <>
              <button className="button primary" type="button" onClick={props.onSettings}>Set audit actor</button>
              <button className="button" type="button" disabled>Publish Release</button>
            </>
          )}
        </div>
      </section>

      <div className="detailLayout">
        <div className="detailMain">
          <section className="workspacePanel">
            <PanelHeader eyebrow="Active contract" title={app?.app_key || "No active contract"} description={app?.entrypoint || "Publish a release to make this app visible to workers."} />
            <ContractEvidence app={app} detail={props.detail} />
          </section>

          <section className="workspacePanel">
            <PanelHeader eyebrow="Release history" title="Audit trail" description="Each release records the published commit, actor, release id, and note." />
            <LatestAudit history={props.history} title="Release audit" />
          </section>

          <section className="workspacePanel">
            <PanelHeader eyebrow="Repository snapshot" title="Materialized files" description="The active contract was generated from this repository snapshot." />
            <SourceSnapshotPanel files={props.sourceFiles} compact />
          </section>
        </div>

        <aside className="detailAside">
          <section className="workspacePanel">
            <PanelHeader eyebrow="Repository settings" title="Git source" description={source.kind || "git"} />
            <div className="sourceDetailGrid">
              <Field label="Branch" value={source.branch || "main"} />
              <Field label="Subpath" value={source.subpath || "root"} />
              <Field label="Credential" value={source.creds_ref ? "configured" : "public repository"} />
              <Field label="Repository ID" value={String(source.id)} />
              <Field label="Last release" value={formatDate(source.last_synced_at)} />
              <CopyField label="Last commit" value={source.last_synced_commit || ""} display={shortID(source.last_synced_commit, 16)} />
            </div>
          </section>

          <section className="workspacePanel">
            <ReadinessPanel source={source} app={app} actor={props.actor} liveWorkers={props.liveWorkers} />
          </section>

          <section className="workspacePanel dangerZonePanel">
            <div>
              <strong>App registration</strong>
              <p>Remove only when this app should no longer be publishable from the control plane.</p>
            </div>
            <button className="button dangerGhost" type="button" onClick={() => props.onRemove(source)}>Remove App</button>
          </section>
        </aside>
      </div>
    </div>
  );
}

function ContractEvidence({ app, detail }: { app: AppSummary | null; detail: AppDetail | null }) {
  if (!app) {
    return <div id="actionList" className="emptyState"><strong>No active contract</strong><p>Publish this app first.</p></div>;
  }
  return (
    <div className="contractTab">
      <div className="evidenceGrid">
        <Field label="App key" value={app.app_key} />
        <Field label="Entrypoint" value={app.entrypoint || "-"} />
        <CopyField label="Commit" value={app.commit_sha} display={shortID(app.commit_sha, 18)} />
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

function CopyField({ label, value, display }: { label: string; value: string; display?: string }) {
  return (
    <div className="kv copyField">
      <span>{label}</span>
      <div>
        <strong title={value || "-"}>{display || value || "-"}</strong>
        <CopyButton value={value} />
      </div>
    </div>
  );
}

function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  async function handleCopy() {
    if (!value) return;
    await copyText(value);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  }
  return (
    <button className={copied ? "copyButton copied" : "copyButton"} type="button" disabled={!value} onClick={() => void handleCopy()}>
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

function DetailNotFound({ title, onBack }: { title: string; onBack: () => void }) {
  return (
    <section className="workspacePanel emptyState">
      <span className="eyebrow">Detail</span>
      <h2>{title}</h2>
      <button className="button primary" type="button" onClick={onBack}>Back to console</button>
    </section>
  );
}

async function copyText(value: string) {
  if (!value) return;
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const element = document.createElement("textarea");
  element.value = value;
  element.style.position = "fixed";
  element.style.opacity = "0";
  document.body.appendChild(element);
  element.select();
  document.execCommand("copy");
  document.body.removeChild(element);
}
