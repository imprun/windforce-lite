import { matchRoute, useRouter } from "./lib/router";
import { AppDetailPage } from "./pages/AppDetailPage";
import { AppsPage } from "./pages/AppsPage";
import { APIClientsPage } from "./pages/APIClientsPage";
import { MonitoringPage } from "./pages/MonitoringPage";
import { SettingsPage } from "./pages/SettingsPage";

export function App() {
  const { path } = useRouter();

  const appDetail = matchRoute("/apps/:id/:tab?", path);
  if (appDetail) {
    const sourceID = Number(appDetail.id);
    if (Number.isFinite(sourceID) && sourceID > 0) {
      return <AppDetailPage sourceID={sourceID} tab={appDetail.tab || "overview"} />;
    }
  }

  if (matchRoute("/monitoring", path)) return <MonitoringPage />;
  if (matchRoute("/api-clients", path)) return <APIClientsPage />;
  // Back-compat: /jobs was the pre-rename route, and /jobs/{id} was the
  // removed per-job detail page (ADR 0005).
  const legacyJobs = matchRoute("/jobs/:id?", path);
  if (legacyJobs) return <MonitoringPage legacyJobID={legacyJobs.id} />;
  if (matchRoute("/settings", path)) return <SettingsPage />;
  return <AppsPage />;
}
