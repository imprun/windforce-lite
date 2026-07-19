import { useMemo, useState } from "react";
import { CheckCircle2, Clipboard, Download, FileInput, Play, RotateCcw, ShieldCheck, Trash2, Upload } from "lucide-react";
import { Layout } from "../components/Layout";
import { SettingsNav } from "../components/SettingsNav";
import { EmptyState, ErrorNotice, Field, Panel } from "../components/ui";
import { errorMessage, type ProvisioningAppliedResource } from "../lib/api";
import { useApp } from "../lib/app-context";

const sampleYaml = `resources:
  - apiVersion: windforce-lite.imprun.dev/v1
    kind: AppSource
    metadata:
      name: example-app
    spec:
      repository:
        url: https://example.test/group/example-app.git
        branch: main
        authRef: example-app-git

  - apiVersion: windforce-lite.imprun.dev/v1
    kind: GitCredential
    metadata:
      name: example-app-git
    spec:
      method: pat
      storageRef: git/example-app/credential
      token:
        valueFrom:
          env: EXAMPLE_APP_GIT_TOKEN
`;

type ImportFormat = "yaml" | "json";
type ExportFormat = "yaml" | "json";
type ProvisioningTask = "import" | "export";

export function ProvisioningPage() {
  const { api, notify, settings } = useApp();
  const [task, setTask] = useState<ProvisioningTask>("import");
  const [exportFormat, setExportFormat] = useState<ExportFormat>("yaml");
  const [includeValues, setIncludeValues] = useState(false);
  const [exportText, setExportText] = useState("");
  const [exporting, setExporting] = useState(false);
  const [importFormat, setImportFormat] = useState<ImportFormat>("yaml");
  const [importText, setImportText] = useState(sampleYaml);
  const [dryRunResult, setDryRunResult] = useState<ProvisioningAppliedResource[]>([]);
  const [applyResult, setApplyResult] = useState<ProvisioningAppliedResource[]>([]);
  const [error, setError] = useState("");
  const [working, setWorking] = useState<"dry-run" | "apply" | "export" | "">("");

  const importReady = importText.trim().length > 0;
  const canApply = importReady && dryRunResult.length > 0 && working === "";
  const resultRows = applyResult.length ? applyResult : dryRunResult;
  const resultLabel = applyResult.length ? "Applied resources" : "Dry-run result";
  const resultSummary = summarizeResult(resultRows);
  const exportFileName = useMemo(
    () => `windforce-lite-${settings.workspace || "default"}-provisioning.${exportFormat === "yaml" ? "yaml" : "json"}`,
    [exportFormat, settings.workspace],
  );

  async function handleExport() {
    setError("");
    setExporting(true);
    setWorking("export");
    try {
      const text = await api.exportProvisioning(exportFormat, includeValues);
      setExportText(text);
      notify("ok", "Provisioning export refreshed.");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setExporting(false);
      setWorking("");
    }
  }

  async function handleDryRun() {
    setError("");
    setApplyResult([]);
    setWorking("dry-run");
    try {
      const result = await api.importProvisioning(importText, true, importFormat);
      setDryRunResult(result.applied || []);
      notify("ok", "Dry-run completed.");
    } catch (cause) {
      setDryRunResult([]);
      setError(errorMessage(cause));
    } finally {
      setWorking("");
    }
  }

  async function handleApply() {
    setError("");
    setWorking("apply");
    try {
      const result = await api.importProvisioning(importText, false, importFormat);
      setApplyResult(result.applied || []);
      setDryRunResult([]);
      notify("ok", "Provisioning applied.");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setWorking("");
    }
  }

  async function copyExport() {
    if (!exportText) return;
    try {
      await navigator.clipboard.writeText(exportText);
      notify("ok", "Export copied.");
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  function downloadExport() {
    if (!exportText) return;
    const blob = new Blob([exportText], { type: exportFormat === "yaml" ? "application/yaml" : "application/json" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = exportFileName;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  async function handleFile(file: File | null) {
    if (!file) return;
    const text = await file.text();
    setImportText(text);
    const lower = file.name.toLowerCase();
    setImportFormat(lower.endsWith(".json") ? "json" : "yaml");
    setDryRunResult([]);
    setApplyResult([]);
    setError("");
  }

  function resetImportDocument(text: string, format: ImportFormat) {
    setImportText(text);
    setImportFormat(format);
    setDryRunResult([]);
    setApplyResult([]);
    setError("");
  }

  return (
    <Layout
      title="Settings"
      subtitle="Provision repeatable control-plane state for backup, restore, and environment setup."
    >
      <SettingsNav />
      {error ? <ErrorNotice message={error} /> : null}

      <div className="provisioningModeTabs" role="tablist" aria-label="Provisioning mode">
        <button
          type="button"
          role="tab"
          aria-selected={task === "import"}
          className={task === "import" ? "active" : ""}
          onClick={() => setTask("import")}
        >
          Import
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={task === "export"}
          className={task === "export" ? "active" : ""}
          onClick={() => setTask("export")}
        >
          Export
        </button>
      </div>

      {task === "import" ? (
        <section className="provisioningWorkspace" aria-label="Import provisioning document">
          <Panel title="Import document" subtitle="Paste or load YAML/JSON. Dry-run must pass before Apply is enabled.">
            <div className="provisioningDocumentHeader">
              <Field label="Format">
                <select
                  aria-label="Import format"
                  value={importFormat}
                  onChange={(event) => {
                    setImportFormat(event.target.value as ImportFormat);
                    setDryRunResult([]);
                    setApplyResult([]);
                  }}
                >
                  <option value="yaml">YAML</option>
                  <option value="json">JSON</option>
                </select>
              </Field>
              <label className="button">
                <FileInput aria-hidden="true" />
                Load file
                <input
                  className="visuallyHidden"
                  type="file"
                  accept=".yaml,.yml,.json,application/json,application/yaml,text/yaml"
                  onChange={(event) => void handleFile(event.target.files?.[0] || null)}
                />
              </label>
              <button className="button" type="button" onClick={() => resetImportDocument(sampleYaml, "yaml")}>
                <RotateCcw aria-hidden="true" />
                Reset sample
              </button>
            </div>
            <Field label="Provisioning document">
              <textarea
                className="provisioningEditor"
                value={importText}
                spellCheck={false}
                onChange={(event) => {
                  setImportText(event.target.value);
                  setDryRunResult([]);
                  setApplyResult([]);
                }}
              />
            </Field>
          </Panel>

          <aside className="provisioningSidePanel" aria-label="Import controls and result">
            <Panel title="Review and apply" subtitle="Validate the document, then apply the reviewed result.">
              <div className="provisioningActionStack">
                <button className="button" type="button" disabled={!importReady || working !== ""} onClick={handleDryRun}>
                  <Play aria-hidden="true" />
                  {working === "dry-run" ? "Checking…" : "Dry-run"}
                </button>
                <button className="button primary" type="button" disabled={!canApply} onClick={handleApply}>
                  <Upload aria-hidden="true" />
                  {working === "apply" ? "Applying…" : "Apply"}
                </button>
                <button
                  className="button"
                  type="button"
                  disabled={!importText || working !== ""}
                  onClick={() => resetImportDocument("", importFormat)}
                >
                  <Trash2 aria-hidden="true" />
                  Clear document
                </button>
              </div>
              <div className="provisioningSafety">
                <ShieldCheck aria-hidden="true" />
                <span>Dry-run checks the document without changing stored state.</span>
              </div>
            </Panel>

            <Panel
              title={resultLabel}
              subtitle={resultRows.length ? resultSummary : "Run dry-run to review planned resources before applying."}
            >
              {resultRows.length ? (
                <ProvisioningResultList rows={resultRows} />
              ) : (
                <EmptyState title="No validation result">
                  <span>Run dry-run to review what would be created, updated, stored, or validated.</span>
                </EmptyState>
              )}
            </Panel>
          </aside>
        </section>
      ) : (
        <section className="provisioningWorkspace" aria-label="Export provisioning snapshot">
          <Panel title="Export preview" subtitle="Create and review a redacted workspace snapshot.">
            {exportText ? (
              <>
                <div className="provisioningToolbar">
                  <span className="cellSub">{exportFileName}</span>
                </div>
                <pre className="provisioningCode">{exportText}</pre>
              </>
            ) : (
              <EmptyState title="No snapshot exported">
                <span>Export a snapshot to preview, copy, or download the current workspace state.</span>
              </EmptyState>
            )}
          </Panel>

          <aside className="provisioningSidePanel" aria-label="Export controls">
            <Panel title="Snapshot options" subtitle="Secrets and credential values are always redacted.">
              <div className="formStack">
                <Field label="Format">
                  <select
                    aria-label="Export format"
                    value={exportFormat}
                    onChange={(event) => setExportFormat(event.target.value as ExportFormat)}
                  >
                    <option value="yaml">YAML</option>
                    <option value="json">JSON</option>
                  </select>
                </Field>
                <label className="toggleField">
                  <input
                    type="checkbox"
                    checked={includeValues}
                    onChange={(event) => setIncludeValues(event.target.checked)}
                  />
                  <span>
                    Include non-secret values
                    <small>Secret variables and credential values remain redacted.</small>
                  </span>
                </label>
              </div>
              <div className="provisioningActionStack">
                <button className="button primary" type="button" onClick={handleExport} disabled={working === "export"}>
                  <Download aria-hidden="true" />
                  {exporting ? "Exporting…" : "Export snapshot"}
                </button>
                <button className="button" type="button" onClick={copyExport} disabled={!exportText}>
                  <Clipboard aria-hidden="true" />
                  Copy
                </button>
                <button className="button" type="button" onClick={downloadExport} disabled={!exportText}>
                  <Download aria-hidden="true" />
                  Download
                </button>
              </div>
            </Panel>
          </aside>
        </section>
      )}
    </Layout>
  );
}

function ProvisioningResultList({ rows }: { rows: ProvisioningAppliedResource[] }) {
  return (
    <div className="provisioningResultList">
      {rows.map((row, index) => (
        <div className="provisioningResultItem" key={`${row.kind}-${row.name}-${index}`}>
          <div>
            <span className="cellTitle">{row.name}</span>
            <span className="cellSub">
              {row.kind}
              {row.detail ? ` · ${row.detail}` : ""}
            </span>
          </div>
          <span className={row.action === "validated" ? "badge badge-running" : "badge badge-good"}>
            <CheckCircle2 aria-hidden="true" />
            {row.action}
          </span>
        </div>
      ))}
    </div>
  );
}

function summarizeResult(rows: ProvisioningAppliedResource[]): string {
  if (!rows.length) return "";
  const counts = rows.reduce<Record<string, number>>((acc, row) => {
    acc[row.action] = (acc[row.action] || 0) + 1;
    return acc;
  }, {});
  return Object.entries(counts)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([action, count]) => `${count} ${action}`)
    .join(" · ");
}
