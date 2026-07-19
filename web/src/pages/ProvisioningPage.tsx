import { useMemo, useState } from "react";
import { CheckCircle2, Clipboard, Download, FileInput, Play, ShieldCheck, Upload } from "lucide-react";
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

export function ProvisioningPage() {
  const { api, notify, settings } = useApp();
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

  return (
    <Layout
      title="Settings"
      subtitle="Provision repeatable control-plane state for backup, restore, and environment setup."
      actions={
        <button className="button primary" type="button" onClick={handleExport} disabled={exporting}>
          <Download aria-hidden="true" />
          Export snapshot
        </button>
      }
    >
      <SettingsNav />
      {error ? <ErrorNotice message={error} /> : null}

      <section className="provisioningTaskGrid" aria-label="Provisioning tasks">
        <div className="provisioningTask">
          <div>
            <span className="eyebrow">Export</span>
            <h2>Workspace snapshot</h2>
            <p>Create a redacted provisioning document for review, backup, or another environment.</p>
          </div>
          <div className="provisioningTaskControls">
            <select
              aria-label="Export format"
              value={exportFormat}
              onChange={(event) => setExportFormat(event.target.value as ExportFormat)}
            >
              <option value="yaml">YAML</option>
              <option value="json">JSON</option>
            </select>
            <button className="button primary" type="button" onClick={handleExport} disabled={working === "export"}>
              <Download aria-hidden="true" />
              {exporting ? "Exporting…" : "Export"}
            </button>
          </div>
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

        <div className="provisioningTask">
          <div>
            <span className="eyebrow">Import</span>
            <h2>Validate before apply</h2>
            <p>Load or paste a provisioning document. Apply is guarded until the dry-run succeeds.</p>
          </div>
          <div className="provisioningTaskControls">
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
          </div>
          <div className="provisioningTaskControls">
            <button className="button" type="button" disabled={!importReady || working !== ""} onClick={handleDryRun}>
              <Play aria-hidden="true" />
              Dry-run
            </button>
            <button className="button primary" type="button" disabled={!canApply} onClick={handleApply}>
              <Upload aria-hidden="true" />
              Apply
            </button>
          </div>
          <div className="provisioningSafety">
            <ShieldCheck aria-hidden="true" />
            <span>Dry-run checks the document without changing stored state.</span>
          </div>
        </div>
      </section>

      {exportText ? (
        <Panel
          title="Export preview"
          subtitle="Review or save the latest exported snapshot."
          actions={
            <div className="inlineActions">
              <button className="button small" type="button" onClick={copyExport}>
                <Clipboard aria-hidden="true" />
                Copy
              </button>
              <button className="button small" type="button" onClick={downloadExport}>
                <Download aria-hidden="true" />
                Download
              </button>
            </div>
          }
        >
          <div className="provisioningToolbar">
            <span className="cellSub">{exportFileName}</span>
          </div>
          <pre className="provisioningCode">{exportText}</pre>
        </Panel>
      ) : null}

      <Panel title="Import document" subtitle="Use valueFrom.env or valueFrom.file for credentials and environment-specific values.">
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

      <Panel title={resultLabel} subtitle={resultRows.length ? resultSummary : "Run dry-run to review planned resources before applying."}>
        {resultRows.length ? (
          <div className="tableWrap">
            <table className="table provisioningResultTable">
              <thead>
                <tr>
                  <th>Kind</th>
                  <th>Name</th>
                  <th>Action</th>
                  <th>Detail</th>
                </tr>
              </thead>
              <tbody>
                {resultRows.map((row, index) => (
                  <tr key={`${row.kind}-${row.name}-${index}`}>
                    <td>
                      <span className="cellTitle">{row.kind}</span>
                    </td>
                    <td>{row.name}</td>
                    <td>
                      <span className={row.action === "validated" ? "badge badge-running" : "badge badge-good"}>
                        <CheckCircle2 aria-hidden="true" />
                        {row.action}
                      </span>
                    </td>
                    <td className="mono">{row.detail || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState title="No validation result">
            <span>Run dry-run to review what would be created, updated, stored, or validated.</span>
          </EmptyState>
        )}
      </Panel>
    </Layout>
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
