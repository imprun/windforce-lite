import { matchRoute, useRouter } from "./lib/router";
import { AppDetailPage } from "./pages/AppDetailPage";
import { AppsPage } from "./pages/AppsPage";
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
  // Back-compat for the pre-rename bookmark.
  if (matchRoute("/jobs", path)) return <MonitoringPage />;
  if (matchRoute("/settings", path)) return <SettingsPage />;
  return <AppsPage />;
}
