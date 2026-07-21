import { useState } from "react";
import { Field, Modal } from "../components/ui";
import { type Client, errorMessage } from "../lib/api";
import { useApp } from "../lib/app-context";

export function ClientDialog({
  client,
  onClose,
  onSaved,
  onDeleted,
}: {
  client?: Client;
  onClose: () => void;
  onSaved: () => void;
  onDeleted: () => void;
}) {
  const { api, notify } = useApp();
  const [name, setName] = useState(client?.name || "");
  const [hasToken, setHasToken] = useState(client?.has_token || false);
  const [issuedToken, setIssuedToken] = useState("");
  const [pendingRefresh, setPendingRefresh] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const normalizedName = name.trim();
  const dirty = !client || normalizedName !== client.name;

  function finish() {
    if (pendingRefresh) onSaved();
    else onClose();
  }

  function close() {
    if (
      issuedToken &&
      !window.confirm("This API token is shown only once. Close without copying it?")
    ) {
      return;
    }
    finish();
  }

  async function save() {
    if (!normalizedName) {
      setError("Name is required.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (client) {
        await api.updateClient(client.id, { name: normalizedName });
        notify("ok", `Updated ${normalizedName}.`);
        onSaved();
      } else {
        const result = await api.createClient({ name: normalizedName });
        setIssuedToken(result.api_token);
        setHasToken(result.client.has_token);
        setPendingRefresh(true);
        notify("ok", `Created ${normalizedName}.`);
      }
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function rotateToken() {
    if (!client) return;
    if (
      hasToken &&
      !window.confirm("Rotate this client token? The current token will stop working immediately.")
    ) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await api.rotateClientToken(client.id);
      setIssuedToken(result.api_token);
      setHasToken(true);
      setPendingRefresh(true);
      notify("ok", `Issued a new API token for ${client.name}.`);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function revokeToken() {
    if (!client || !hasToken) return;
    if (
      !window.confirm("Revoke this client token? Public API calls will stop working immediately.")
    ) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.revokeClientToken(client.id);
      setHasToken(false);
      setPendingRefresh(true);
      notify("ok", `Revoked the API token for ${client.name}.`);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function copyToken() {
    try {
      await navigator.clipboard.writeText(issuedToken);
      notify("ok", "Copied the client API token.");
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  async function remove() {
    if (!client) return;
    if (hasToken) {
      setError("Revoke the active API token before deleting this client.");
      return;
    }
    if (!window.confirm(`Delete client ${client.name}?`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteClient(client.id);
      notify("ok", `Deleted ${client.name}.`);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  if (issuedToken) {
    return (
      <Modal title="Save client API token" onClose={close}>
        <div className="inlineNotice">
          This token is shown only once. Store it in the calling system before closing this dialog.
        </div>
        <Field label="API token">
          <input
            className="mono"
            readOnly
            value={issuedToken}
            onFocus={(event) => event.currentTarget.select()}
          />
        </Field>
        {error ? <div className="inlineNotice error">{error}</div> : null}
        <footer className="dialogFooter">
          <span />
          <div className="dialogFooterActions">
            <button className="button" type="button" onClick={() => void copyToken()}>
              Copy token
            </button>
            <button className="button primary" type="button" onClick={finish}>
              Done
            </button>
          </div>
        </footer>
      </Modal>
    );
  }

  return (
    <Modal title={client ? "Edit Client" : "Register Client"} onClose={close}>
      <div className="formGrid">
        <Field label="Name">
          <input maxLength={200} value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        {client ? (
          <Field label="Public API token">
            <div>
              <p>{hasToken ? "Active" : "Not issued"}</p>
              <div className="dialogFooterActions">
                <button className="button" type="button" disabled={busy} onClick={rotateToken}>
                  {hasToken ? "Rotate token" : "Issue token"}
                </button>
                <button
                  className="button danger"
                  type="button"
                  disabled={busy || !hasToken}
                  onClick={revokeToken}
                >
                  Revoke token
                </button>
              </div>
              <p className="fieldHint">
                The bearer grants access only to this client&apos;s public API routes in this
                workspace.
              </p>
            </div>
          </Field>
        ) : (
          <div className="inlineNotice">
            A client API token will be generated and shown once after registration.
          </div>
        )}
      </div>
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <footer className="dialogFooter">
        <span>
          {client ? (
            <button
              className="button danger"
              type="button"
              disabled={busy || hasToken}
              onClick={remove}
            >
              Delete
            </button>
          ) : null}
        </span>
        <div className="dialogFooterActions">
          <button className="button" type="button" disabled={busy} onClick={close}>
            Cancel
          </button>
          <button
            className="button primary"
            type="button"
            disabled={busy || !dirty || !normalizedName}
            onClick={save}
          >
            {busy ? "Saving…" : client ? "Save changes" : "Create client"}
          </button>
        </div>
      </footer>
    </Modal>
  );
}
