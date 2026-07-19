import { useEffect, useMemo, useState, type ReactNode } from "react";
import { CheckCircle2, CircleAlert, ServerCog } from "lucide-react";
import { Layout } from "../components/Layout";
import { SettingsNav } from "../components/SettingsNav";
import { DefinitionList, ErrorNotice, Loading, Panel } from "../components/ui";
import { errorMessage, type SystemInfo } from "../lib/api";
import { useApp } from "../lib/app-context";

export function SettingsInfoPage() {
  const { api, settings } = useApp();
  const [info, setInfo] = useState<SystemInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const browserItems = useMemo<Array<[string, ReactNode]>>(
    () => [
      ["Workspace", settings.workspace || "default"],
      ["Actor", settings.actor || "(not set)"],
      ["API token", settings.token ? "configured in this browser" : "not configured"],
      ["API base", `/api/w/${encodeURIComponent(settings.workspace || "default")}`],
    ],
    [settings],
  );

  async function loadInfo() {
    setLoading(true);
    setError("");
    try {
      setInfo(await api.systemInfo());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    api.systemInfo()
      .then((data) => {
        if (active) setInfo(data);
      })
      .catch((cause) => {
        if (active) setError(errorMessage(cause));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [api]);

  return (
    <Layout
      title="Settings"
      subtitle="Read-only service information and browser-local Web UI settings."
      actions={
        <button className="button" type="button" onClick={() => void loadInfo()} title="Refresh service information">
          <ServerCog aria-hidden="true" />
          Refresh
        </button>
      }
    >
      <SettingsNav />
      {error ? <ErrorNotice message={error} onRetry={() => void loadInfo()} /> : null}
      {loading && !info ? <Loading label="Loading service information…" /> : null}
      {info ? (
        <>
          <Panel title="Service" subtitle="Backend service identity and readiness reported by the control plane.">
            <div className="settingsInfoHero">
              <div className={info.ready ? "settingsInfoStatus good" : "settingsInfoStatus warning"}>
                {info.ready ? <CheckCircle2 aria-hidden="true" /> : <CircleAlert aria-hidden="true" />}
                <div>
                  <strong>{info.ready ? "Ready" : "Not ready"}</strong>
                  <span>{info.service}</span>
                </div>
              </div>
              <DefinitionList
                items={[
                  ["Service", info.service],
                  ["Workspace", info.workspace],
                  ["Readiness", info.ready ? "ready" : "not ready"],
                ]}
              />
            </div>
          </Panel>

          <div className="settingsInfoGrid">
            <Panel title="Enabled surfaces" subtitle="Request planes and public handlers enabled in this process.">
              <FlagList values={info.planes} />
            </Panel>
            <Panel title="Backend availability" subtitle="Backend integrations available to this control-plane process.">
              <FlagList values={info.backends} />
            </Panel>
          </div>

          <div className="settingsInfoGrid">
            <Panel title="Authentication and secrets" subtitle="Only configured/not configured is shown; secret values are never exposed.">
              <FlagList values={info.auth} />
            </Panel>
            <Panel title="Runtime configuration" subtitle="Non-secret runtime settings useful for local and operational diagnosis.">
              <DefinitionList items={Object.entries(info.runtime_config).map(([key, value]) => [labelize(key), formatSystemInfoValue(value)])} />
            </Panel>
          </div>
        </>
      ) : null}

      <Panel title="Web UI browser settings" subtitle="Local values this browser sends with control-plane requests.">
        <DefinitionList items={browserItems} />
      </Panel>
    </Layout>
  );
}

function FlagList({ values }: { values: Record<string, boolean> }) {
  const entries = Object.entries(values).sort(([left], [right]) => left.localeCompare(right));
  return (
    <div className="settingsInfoFlags">
      {entries.map(([key, enabled]) => (
        <div className="settingsInfoFlag" key={key}>
          <span className={enabled ? "badge badge-good" : "badge badge-neutral"}>{enabled ? "Enabled" : "Not enabled"}</span>
          <strong>{labelize(key)}</strong>
        </div>
      ))}
    </div>
  );
}

function labelize(key: string): string {
  return key
    .split("_")
    .filter(Boolean)
    .map((part) => part[0].toUpperCase() + part.slice(1))
    .join(" ");
}

export function formatSystemInfoValue(value: unknown): string {
  if (typeof value === "boolean") return value ? "Enabled" : "Not enabled";
  if (value === null || value === undefined || value === "") return "—";
  return String(value);
}
