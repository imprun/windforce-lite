import { useMemo, useState } from "react";
import { CheckCircle2, Clipboard, Download, FileInput, Play, ShieldCheck, Upload } from "lucide-react";
import { Layout } from "../components/Layout";
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
      title="Provisioning"
      subtitle="Import and export repeatable control-plane state for this workspace."
      actions={
        <button className="button primary" type="button" onClick={handleExport} disabled={exporting}>
          <Download aria-hidden="true" />
          Export current state
        </button>
      }
    >
      {error ? <ErrorNotice message={error} /> : null}

      <div className="provisioningGrid">
        <Panel
          title="Export"
          subtitle="Create a redacted workspace snapshot that can be reviewed, copied, or saved."
          actions={
            <div className="inlineActions">
              <select
                aria-label="Export format"
                value={exportFormat}
                onChange={(event) => setExportFormat(event.target.value as ExportFormat)}
              >
                <option value="yaml">YAML</option>
                <option value="json">JSON</option>
              </select>
              <button className="button" type="button" onClick={handleExport} disabled={working === "export"}>
                <Download aria-hidden="true" />
                Refresh
              </button>
            </div>
          }
        >
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
          {exportText ? (
            <>
              <div className="provisioningToolbar">
                <span className="cellSub">{exportFileName}</span>
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
              </div>
              <pre className="provisioningCode">{exportText}</pre>
            </>
          ) : (
            <EmptyState title="No export loaded">
              <button className="button" type="button" onClick={handleExport} disabled={exporting}>
                <Download aria-hidden="true" />
                Export workspace
              </button>
            </EmptyState>
          )}
        </Panel>

        <Panel
          title="Import"
          subtitle="Paste or load a provisioning file. Validate with dry-run before applying."
          actions={
            <div className="inlineActions">
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
          }
        >
          <Field label="Provisioning document" hint="Use valueFrom.env or valueFrom.file for credentials and environment-specific values.">
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
          <div className="provisioningApplyBar">
            <div className="provisioningSafety">
              <ShieldCheck aria-hidden="true" />
              <span>Apply is enabled after a successful dry-run.</span>
            </div>
            <div className="inlineActions">
              <button className="button" type="button" disabled={!importReady || working !== ""} onClick={handleDryRun}>
                <Play aria-hidden="true" />
                Dry-run
              </button>
              <button className="button primary" type="button" disabled={!canApply} onClick={handleApply}>
                <Upload aria-hidden="true" />
                Apply
              </button>
            </div>
          </div>
        </Panel>
      </div>

      <Panel title={resultLabel} subtitle="Resources are listed in the order returned by the control plane.">
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
