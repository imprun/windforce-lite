import { useState } from "react";
import { Field, Modal } from "../components/ui";
import { errorMessage, type APIClient } from "../lib/api";
import { useApp } from "../lib/app-context";

export function APIClientDialog({
  client,
  onClose,
  onSaved,
  onDeleted,
}: {
  client?: APIClient;
  onClose: () => void;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const { api, notify } = useApp();
  const [name, setName] = useState(client?.name || "");
  const [clientKey, setClientKey] = useState(client?.client_key || "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const normalizedName = name.trim();
  const normalizedKey = clientKey.trim();
  const keyValid = normalizedKey !== "" && !/\s/u.test(normalizedKey);
  const dirty = !client || normalizedName !== client.name || normalizedKey !== client.client_key;

  async function save() {
    if (!normalizedName) {
      setError("Name is required.");
      return;
    }
    if (!keyValid) {
      setError("Client key is required and must not contain whitespace.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (client) {
        await api.updateAPIClient(client.id, { name: normalizedName, client_key: normalizedKey });
        notify("ok", `Updated ${normalizedName}.`);
      } else {
        await api.createAPIClient({ name: normalizedName, client_key: normalizedKey });
        notify("ok", `Created ${normalizedName}.`);
      }
      onSaved();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!client) return;
    if (!window.confirm(`Delete API client ${client.name}?`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteAPIClient(client.id);
      notify("ok", `Deleted ${client.name}.`);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={client ? "Edit API Client" : "Create API Client"} onClose={onClose}>
      <div className="formGrid">
        <Field label="Name">
          <input autoFocus maxLength={200} value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        <Field label="Client key">
          <input
            className="mono"
            maxLength={512}
            autoComplete="off"
            spellCheck={false}
            value={clientKey}
            onChange={(event) => setClientKey(event.target.value)}
          />
        </Field>
      </div>
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <footer className="dialogFooter">
        <span>
          {client ? (
            <button className="button danger" type="button" disabled={busy} onClick={remove}>
              Delete
            </button>
          ) : null}
        </span>
        <div className="dialogFooterActions">
          <button className="button" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button
            className="button primary"
            type="button"
            disabled={busy || !dirty || !normalizedName || !keyValid}
            onClick={save}
          >
            {busy ? "Saving…" : client ? "Save changes" : "Create client"}
          </button>
        </div>
      </footer>
    </Modal>
  );
}
