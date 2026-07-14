import { lazy, Suspense } from "react";
import { matchRoute, useRouter } from "./lib/router";
import { AppDetailPage } from "./pages/AppDetailPage";
import { AppsPage } from "./pages/AppsPage";
import { ClientRegistryPage } from "./pages/ClientRegistryPage";
import { MonitoringPage } from "./pages/MonitoringPage";
import { SettingsPage } from "./pages/SettingsPage";

const OpenAPIViewerPage = lazy(async () => {
  const module = await import("./pages/OpenAPIViewerPage");
  return { default: module.OpenAPIViewerPage };
});

export function App() {
  const { path } = useRouter();

  const openAPI = matchRoute("/openapi/:workspace/:app", path);
  if (openAPI) {
    return (
      <Suspense fallback={<p className="loading">Loading OpenAPI reference…</p>}>
        <OpenAPIViewerPage workspace={openAPI.workspace} appKey={openAPI.app} />
      </Suspense>
    );
  }

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
  if (matchRoute("/clients", path)) return <ClientRegistryPage />;
  // Back-compat: /jobs was the pre-rename route, and /jobs/{id} was the
  // removed per-job detail page (ADR 0005).
  const legacyJobs = matchRoute("/jobs/:id?", path);
  if (legacyJobs) return <MonitoringPage legacyJobID={legacyJobs.id} />;
  if (matchRoute("/settings", path)) return <SettingsPage />;
  return <AppsPage />;
}
