"use client";

import { useCallback, useEffect, useMemo, useState, type ComponentProps, type ReactNode } from "react";
import type { AppDetail, AppHistoryItem, AppSummary } from "@/entities/app";
import type { GitSource } from "@/entities/git-source";
import { SettingsPage } from "@/features/control-plane-settings/ui/SettingsPage";
import { DeploySourceDialog } from "@/features/source-deployment/ui/DeploySourceDialog";
import { SourceRegistrationForm } from "@/features/source-registration/ui/SourceRegistrationForm";
import { Sidebar } from "@/widgets/sidebar";
import { Topbar } from "@/widgets/topbar";
import { WindforceApi } from "@/shared/api/client";
import type { ApiSettings, VariableRow, WorkerTagsResponse } from "@/shared/api/types";
import { shortID } from "@/shared/lib/format";
import { AppsSection, AuditSection, ReleasesSection } from "./DeploymentPanels";
import { AppDetailSection } from "./DeploymentDetailPages";
import type { ConsoleSection, DetailPage, DetailTab, Notice } from "./types";

const defaultSettings: ApiSettings = {
  workspace: "default",
  token: "",
  actor: "local-dev",
};

const sectionCopy: Record<ConsoleSection, { title: string; subtitle: string }> = {
  apps: {
    title: "Apps",
    subtitle: "Register apps, review repository settings, and publish worker-visible releases.",
  },
  releases: {
    title: "Active Contracts",
    subtitle: "Review the app/action contracts currently visible to workers.",
  },
  audit: {
    title: "Release History",
    subtitle: "Track who published which commit and which repository snapshot produced the release.",
  },
  settings: {
    title: "Control Plane Settings",
    subtitle: "Set workspace, API token, and audit actor for control-plane operations.",
  },
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
  const [activeSection, setActiveSection] = useState<ConsoleSection>("apps");
  const [detailPage, setDetailPage] = useState<DetailPage | null>(null);
  const [detailTab, setDetailTab] = useState<DetailTab>("contract");
  const [search, setSearch] = useState("");
  const [registrationOpen, setRegistrationOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  useEffect(() => {
    const saved = globalThis.localStorage;
    setSettings({
      workspace: saved.getItem("wf.workspace") || "default",
      token: saved.getItem("wf.token") || "",
      actor: saved.getItem("wf.actor") || "local-dev",
    });
    setSidebarCollapsed(saved.getItem("wf.sidebarCollapsed") === "true");
  }, []);

  useEffect(() => {
    function syncDetailFromURL() {
      setDetailPage(readDetailPageFromLocation());
    }
    syncDetailFromURL();
    window.addEventListener("popstate", syncDetailFromURL);
    return () => window.removeEventListener("popstate", syncDetailFromURL);
  }, []);

  useEffect(() => {
    localStorage.setItem("wf.workspace", settings.workspace);
    localStorage.setItem("wf.token", settings.token);
    localStorage.setItem("wf.actor", settings.actor);
  }, [settings]);

  useEffect(() => {
    localStorage.setItem("wf.sidebarCollapsed", String(sidebarCollapsed));
  }, [sidebarCollapsed]);

  useEffect(() => {
    if (!notice.text || notice.tone === "error") return;
    const timer = window.setTimeout(() => setNotice((current) => current.text === notice.text ? { tone: "info", text: "" } : current), 2400);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const api = useMemo(() => new WindforceApi(settings), [settings]);
  const selectedSource = sources.find((source) => source.id === selectedSourceID) || null;
  const selectedApp = selectedSource
    ? apps.find((app) => app.git_source_id === selectedSource.id) || null
    : apps.find((app) => app.app_key === selectedAppKey) || null;
  const liveWorkers = (workerTags.tags || []).reduce((total, tag) => total + Number(tag.live_workers || 0), 0);
  const credentialCount = variables.filter((item) => item.path?.startsWith("git/")).length;

  const refresh = useCallback(async () => {
    setBusy(true);
    setNotice({ tone: "info", text: "Refreshing app release state..." });
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
      setNotice({ tone: "ok", text: "App release state refreshed." });
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
    if (detailPage?.kind === "app" && sources.some((source) => source.id === detailPage.sourceID)) {
      setSelectedSourceID(detailPage.sourceID);
    }
  }, [detailPage, sources]);

  useEffect(() => {
    const appKey = selectedApp?.app_key || (selectedSource ? "" : selectedAppKey);
    if (!appKey) {
      setAppDetail(null);
      setHistory([]);
      setSourceFiles({});
      return;
    }
    let canceled = false;
    async function loadApp() {
      try {
        const [detail, nextHistory, source] = await Promise.all([
          api.app(appKey),
          api.appHistory(appKey),
          api.appSource(appKey).catch(() => ({ files: {} })),
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
  }, [api, selectedApp?.app_key, selectedAppKey]);

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

  function navigate(section: ConsoleSection) {
    setDetailPage(null);
    writeDetailPageToHistory(null);
    setActiveSection(section);
    if (section === "releases") setDetailTab("contract");
    if (section === "audit") setDetailTab("history");
  }

  function openSettingsPage() {
    navigate("settings");
  }

  function saveSettings(nextSettings: ApiSettings) {
    setSettings(nextSettings);
    setNotice({ tone: "ok", text: "Settings saved." });
  }

  function openSourceDetail(sourceID: number) {
    const next = { kind: "app", sourceID } satisfies DetailPage;
    setActiveSection("apps");
    setSelectedSourceID(sourceID);
    setDetailPage(next);
    writeDetailPageToHistory(next);
  }

  function closeDetailPage() {
    setDetailPage(null);
    writeDetailPageToHistory(null);
  }

  async function deploySelectedSource(message: string) {
    if (!deploySource) return;
    const source = deploySource;
    setDeployError("");
    await run(`Publishing ${source.name}`, async () => {
      const result = await api.deployGitSource(source.id, message ? { confirm: true, message } : { confirm: true });
      setDeploySource(null);
      setSelectedSourceID(source.id);
      setSelectedAppKey(result.app);
      setDetailTab("history");
      const next = { kind: "app", sourceID: source.id } satisfies DetailPage;
      setDetailPage(next);
      writeDetailPageToHistory(next);
      await refresh();
      return `Published ${result.app} at ${shortID(result.commit, 12)}.`;
    });
  }

  const removeSource = (source: GitSource) => run(`Removing ${source.name}`, async () => {
    const confirmed = globalThis.confirm(`Remove app ${source.name}? Release history remains in the audit trail.`);
    if (!confirmed) return "Remove canceled.";
    await api.deleteGitSource(source.id);
    await refresh();
    return `Removed ${source.name}.`;
  });

  const topbarCopy = sectionCopy[activeSection];
  const commonProps = {
    sources,
    apps,
    selectedSourceID,
    selectedSource,
    selectedApp,
    detail: appDetail,
    history,
    sourceFiles,
    search,
    activeTab: detailTab,
    actor: settings.actor,
    liveWorkers,
    credentialCount,
    detailPage,
    onSearch: setSearch,
    onSelectSource: setSelectedSourceID,
    onRegister: () => setRegistrationOpen(true),
    onDeploySource: setDeploySource,
    onOpenSourceDetail: openSourceDetail,
    onBackToList: closeDetailPage,
    onRemove: removeSource,
    onTabChange: setDetailTab,
    onSettings: openSettingsPage,
  } satisfies ComponentProps<typeof AppsSection>;
  const detailSheetCopy = detailPage ? detailCopy(detailPage, sources) : null;

  return (
    <main className={sidebarCollapsed ? "appShell sidebarCollapsed" : "appShell"}>
      <Sidebar
        active={activeSection}
        collapsed={sidebarCollapsed}
        sourceCount={sources.length}
        appCount={apps.length}
        credentialCount={credentialCount}
        liveWorkers={liveWorkers}
        onNavigate={navigate}
        onToggleCollapsed={() => setSidebarCollapsed((current) => !current)}
      />
      <section className="mainArea">
        <Topbar
          title={topbarCopy.title}
          subtitle={topbarCopy.subtitle}
        />

        <div className="content">
          {notice.text && notice.tone === "error" ? <div id="notice" className={`notice ${notice.tone}`}>{notice.text}</div> : null}
          {activeSection === "settings" && !detailPage ? (
            <SettingsPage
              settings={settings}
              sourceCount={sources.length}
              appCount={apps.length}
              credentialCount={credentialCount}
              liveWorkers={liveWorkers}
              busy={busy}
              onSave={saveSettings}
              onRefresh={refresh}
            />
          ) : (
            <ActiveSection section={activeSection} {...commonProps} />
          )}
        </div>
      </section>

      {notice.text && notice.tone !== "error" ? <div id="toast" className={`toast ${notice.tone}`}>{notice.text}</div> : null}

      {registrationOpen ? (
        <div id="registerSourceDialog" className="modalBackdrop" role="presentation">
          <section className="dialog wideDialog" aria-label="Register app">
            <SourceRegistrationForm
              busy={busy}
              onCancel={() => setRegistrationOpen(false)}
              onRegister={(payload) => run("Registering app", async () => {
                const created = await api.registerGitSource(payload);
                setSelectedSourceID(created.id);
                setRegistrationOpen(false);
                await refresh();
                return `Registered ${created.name}.`;
              })}
              onProbe={(payload) => run("Probing repository", async () => {
                const result = await api.probeGitSource(payload);
                return result.reachable ? "Repository is reachable for release validation." : result.error || "Repository is not reachable.";
              })}
              onCreateSample={() => run("Creating sample", async () => {
                await api.sample("echo");
                setRegistrationOpen(false);
                await refresh();
                return "Created sample app.";
              })}
            />
          </section>
        </div>
      ) : null}

      {detailPage && detailSheetCopy ? (
        <DetailSheet title={detailSheetCopy.title} subtitle={detailSheetCopy.subtitle} onClose={closeDetailPage}>
          <AppDetailSection {...commonProps} detailPage={detailPage} />
        </DetailSheet>
      ) : null}

      <DeploySourceDialog
        source={deploySource}
        actor={settings.actor}
        busy={busy}
        error={deployError}
        onClose={() => {
          setDeployError("");
          setDeploySource(null);
        }}
        onDeploy={deploySelectedSource}
        onOpenSettings={openSettingsPage}
      />
    </main>
  );
}

type ActiveSectionProps = ComponentProps<typeof AppsSection> & {
  section: ConsoleSection;
};

function ActiveSection({ section, ...props }: ActiveSectionProps) {
  if (section === "releases") return <ReleasesSection {...props} />;
  if (section === "audit") return <AuditSection {...props} />;
  return <AppsSection {...props} />;
}

function DetailSheet({ title, subtitle, children, onClose }: { title: string; subtitle: string; children: ReactNode; onClose: () => void }) {
  return (
    <div className="sheetLayer" role="presentation">
      <button className="sheetScrim" type="button" aria-label="Close detail sheet" onClick={onClose} />
      <aside className="detailSheet" role="dialog" aria-modal="true" aria-label={title}>
        <header className="sheetHeader">
          <div>
            <span className="eyebrow">Detail sheet</span>
            <h2>{title}</h2>
            <p>{subtitle}</p>
          </div>
          <button className="button compactButton" type="button" onClick={onClose}>Close</button>
        </header>
        <div className="sheetBody">
          {children}
        </div>
      </aside>
    </div>
  );
}

function readDetailPageFromLocation(): DetailPage | null {
  if (typeof window === "undefined") return null;
  const params = new URLSearchParams(window.location.search);
  const detail = params.get("detail");
  if (detail === "app") {
    const sourceID = Number(params.get("app"));
    if (Number.isFinite(sourceID) && sourceID > 0) return { kind: "app", sourceID };
  }
  return null;
}

function writeDetailPageToHistory(detailPage: DetailPage | null) {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  url.searchParams.delete("detail");
  url.searchParams.delete("source");
  url.searchParams.delete("app");
  if (detailPage?.kind === "app") {
    url.searchParams.set("detail", "app");
    url.searchParams.set("app", String(detailPage.sourceID));
  }
  window.history.pushState(null, "", `${url.pathname}${url.search}${url.hash}`);
}

function detailCopy(detailPage: DetailPage, sources: GitSource[]) {
  const source = sources.find((item) => item.id === detailPage.sourceID);
  const state = source?.last_synced_commit ? "released" : "registered";
  return {
    title: source ? `App / ${source.name} / ${state}` : "App detail",
    subtitle: "Inspect repository settings, active contract, release readiness, and audit evidence.",
  };
}
