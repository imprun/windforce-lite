import { matchRoute, useRouter } from "./lib/router";
import { AppDetailPage } from "./pages/AppDetailPage";
import { AppsPage } from "./pages/AppsPage";
import { ClientRegistryPage } from "./pages/ClientRegistryPage";
import { ClientDetailPage } from "./pages/ClientDetailPage";
import { MonitoringPage } from "./pages/MonitoringPage";
import { SettingsPage } from "./pages/SettingsPage";
import { AuditPage } from "./pages/AuditPage";

export function App() {
  const { path } = useRouter();

  const appDetail = matchRoute("/apps/:id/:tab?/:section?/:action?", path);
  if (appDetail) {
    const sourceID = Number(appDetail.id);
    if (Number.isFinite(sourceID) && sourceID > 0) {
      return (
        <AppDetailPage
          sourceID={sourceID}
          tab={appDetail.tab || "overview"}
          section={appDetail.section}
          actionKey={appDetail.action}
        />
      );
    }
  }

  if (matchRoute("/monitoring", path)) return <MonitoringPage />;
  if (matchRoute("/audit", path)) return <AuditPage />;
  const clientDetail = matchRoute("/clients/:id/:tab?/:appKey?", path);
  if (clientDetail?.id) {
    return <ClientDetailPage clientID={clientDetail.id} tab={clientDetail.tab || "overview"} appKey={clientDetail.appKey} />;
  }
  if (matchRoute("/clients", path)) return <ClientRegistryPage />;
  // Back-compat: /jobs was the pre-rename route, and /jobs/{id} was the
  // removed per-job detail page (ADR 0005).
  const legacyJobs = matchRoute("/jobs/:id?", path);
  if (legacyJobs) return <MonitoringPage legacyJobID={legacyJobs.id} />;
  if (matchRoute("/settings", path)) return <SettingsPage />;
  return <AppsPage />;
}
