import { useState } from "react";
import { DefinitionList, Field, Modal, Panel, ProbeNotice } from "../components/ui";
import { errorMessage, type GitSource, type ProbeResult } from "../lib/api";
import { useApp } from "../lib/app-context";
import { gitCredentialSecretValue, type GitAuthMethod } from "../lib/git-credential";
import { formatTime, shortSHA } from "../lib/format";
import {
  probePassed,
  reconnectCredentialPath,
  repositoryAccessLabel,
  repositoryLocationLocked,
} from "../lib/repository-settings";
import { useRouter } from "../lib/router";

type RepositoryAction = "rename" | "branch" | "location" | "credential" | null;

export function RepositorySettings({
  source,
  onChanged,
}: {
  source: GitSource;
  onChanged: () => void;
}) {
  const { api, notify } = useApp();
  const { navigate } = useRouter();
  const [action, setAction] = useState<RepositoryAction>(null);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [probing, setProbing] = useState(false);
  const [error, setError] = useState("");
  const locationLocked = repositoryLocationLocked(source);

  async function probeCurrentRepository() {
    setProbing(true);
    setProbe(null);
    setError("");
    try {
      setProbe(
        await api.probeGitSource({
          repo_url: source.repo_url,
          branch: source.branch || "main",
          subpath: source.subpath || undefined,
          creds_ref: source.creds_ref || undefined,
        }),
      );
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setProbing(false);
    }
  }

  async function removeSource() {
    const confirmed = window.confirm(
      `Remove app ${source.name}? The repository source registration is deleted; release history remains available, and this deletion is recorded in audit.`,
    );
    if (!confirmed) return;
    try {
      await api.deleteGitSource(source.id);
      notify("ok", `Removed ${source.name}.`);
      navigate("/");
    } catch (cause) {
      notify("error", errorMessage(cause));
    }
  }

  function finishChange() {
    setAction(null);
    setProbe(null);
    onChanged();
  }

  return (
    <>
      <Panel
        title="Repository settings"
        subtitle="Release source and access state. Protected values require an explicit change action."
        actions={
          <button className="button" type="button" disabled={probing} onClick={probeCurrentRepository}>
            {probing ? "Probing…" : "Probe repository"}
          </button>
        }
      >
        <DefinitionList
          items={[
            ["Source name", source.name],
            ["Repository URL", source.repo_url],
            ["Branch", source.branch || "main"],
            ["Subpath", source.subpath || "(repo root)"],
            ["Repository access", repositoryAccessLabel(source)],
            ["Kind", source.kind],
            ["Registered", formatTime(source.created_at)],
            ["Latest synchronized source", source.last_synced_commit ? shortSHA(source.last_synced_commit, 16) : "Not synchronized"],
          ]}
        />

        {probe ? <ProbeNotice probe={probe} branch={source.branch || "main"} /> : null}
        {error ? <div className="inlineNotice error">{error}</div> : null}

        <div className="repositoryActions" aria-label="Repository management actions">
          <RepositoryActionRow label="Source name" value={source.name} action="Rename" onClick={() => setAction("rename")} />
          <RepositoryActionRow
            label="Tracked branch"
            value={source.branch || "main"}
            action="Change branch"
            onClick={() => setAction("branch")}
          />
          <RepositoryActionRow
            label="Repository location"
            value={locationLocked ? "Locked after first synchronization" : "Editable before first synchronization"}
            action={locationLocked ? undefined : "Change location"}
            onClick={locationLocked ? undefined : () => setAction("location")}
          />
          <RepositoryActionRow
            label="Repository access"
            value={repositoryAccessLabel(source)}
            action="Reconnect"
            onClick={() => setAction("credential")}
          />
        </div>

        <div className="dangerZone compact">
          <div>
            <strong>Remove source</strong>
            <p>The active release and release history remain available.</p>
          </div>
          <button className="button danger" type="button" onClick={removeSource}>
            Remove source
          </button>
        </div>
      </Panel>

      {action === "rename" ? (
        <RenameSourceDialog source={source} onClose={() => setAction(null)} onChanged={finishChange} />
      ) : null}
      {action === "branch" ? (
        <ChangeBranchDialog source={source} onClose={() => setAction(null)} onChanged={finishChange} />
      ) : null}
      {action === "location" && !locationLocked ? (
        <ChangeLocationDialog source={source} onClose={() => setAction(null)} onChanged={finishChange} />
      ) : null}
      {action === "credential" ? (
        <ReconnectCredentialDialog source={source} onClose={() => setAction(null)} onChanged={finishChange} />
      ) : null}
    </>
  );
}

function RepositoryActionRow({
  label,
  value,
  action,
  onClick,
}: {
  label: string;
  value: string;
  action?: string;
  onClick?: () => void;
}) {
  return (
    <div className="repositoryActionRow">
      <div>
        <strong>{label}</strong>
        <span>{value}</span>
      </div>
      {action && onClick ? (
        <button className="button" type="button" onClick={onClick}>
          {action}
        </button>
      ) : (
        <span className="status muted">Read only</span>
      )}
    </div>
  );
}

function RenameSourceDialog({ source, onClose, onChanged }: RepositoryDialogProps) {
  const { api, notify } = useApp();
  const [name, setName] = useState(source.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function save() {
    const nextName = name.trim();
    if (!nextName) {
      setError("Source name is required.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.patchGitSource(source.id, { name: nextName });
      notify("ok", `Renamed source to ${nextName}.`);
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="Rename source" subtitle="The manifest app key and active release are unchanged." onClose={onClose}>
      <Field label="Source name">
        <input autoFocus value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <DialogActions busy={busy} saveLabel="Rename" saveDisabled={!name.trim() || name.trim() === source.name} onClose={onClose} onSave={save} />
    </Modal>
  );
}

function ChangeBranchDialog({ source, onClose, onChanged }: RepositoryDialogProps) {
  const { api, notify } = useApp();
  const [branch, setBranch] = useState(source.branch || "main");
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [verifiedBranch, setVerifiedBranch] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const verified = verifiedBranch === branch.trim() && probePassed(probe);

  async function verify() {
    setBusy(true);
    setError("");
    setProbe(null);
    try {
      const result = await api.probeGitSource({
        repo_url: source.repo_url,
        branch: branch.trim(),
        subpath: source.subpath || undefined,
        creds_ref: source.creds_ref || undefined,
      });
      setProbe(result);
      setVerifiedBranch(branch.trim());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    setBusy(true);
    setError("");
    try {
      await api.patchGitSource(source.id, { branch: branch.trim() });
      notify("ok", `Tracking branch ${branch.trim()}.`);
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="Change tracked branch" subtitle={source.repo_url} onClose={onClose}>
      <Field label="Branch">
        <input
          autoFocus
          value={branch}
          onChange={(event) => {
            setBranch(event.target.value);
            setProbe(null);
            setVerifiedBranch("");
          }}
        />
      </Field>
      {probe ? <ProbeNotice probe={probe} branch={branch} /> : null}
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <DialogActions
        busy={busy}
        saveLabel="Save branch"
        saveDisabled={!verified || branch.trim() === (source.branch || "main")}
        onClose={onClose}
        onSave={save}
        secondaryLabel="Probe branch"
        onSecondary={verify}
      />
    </Modal>
  );
}

function ChangeLocationDialog({ source, onClose, onChanged }: RepositoryDialogProps) {
  const { api, notify } = useApp();
  const [repoURL, setRepoURL] = useState(source.repo_url);
  const [subpath, setSubpath] = useState(source.subpath);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [verifiedLocation, setVerifiedLocation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const location = `${repoURL.trim()}\n${subpath.trim()}`;
  const verified = verifiedLocation === location && probePassed(probe);

  function changeLocation(nextRepoURL: string, nextSubpath: string) {
    setRepoURL(nextRepoURL);
    setSubpath(nextSubpath);
    setProbe(null);
    setVerifiedLocation("");
  }

  async function verify() {
    setBusy(true);
    setError("");
    setProbe(null);
    try {
      const result = await api.probeGitSource({
        repo_url: repoURL.trim(),
        branch: source.branch || "main",
        subpath: subpath.trim() || undefined,
        creds_ref: source.creds_ref || undefined,
      });
      setProbe(result);
      setVerifiedLocation(location);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    setBusy(true);
    setError("");
    try {
      await api.patchGitSource(source.id, { repo_url: repoURL.trim(), subpath: subpath.trim() });
      notify("ok", "Repository location changed.");
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="Change repository location" subtitle="Available until the first release is published." onClose={onClose}>
      <Field label="Repository URL">
        <input autoFocus value={repoURL} onChange={(event) => changeLocation(event.target.value, subpath)} />
      </Field>
      <Field label="Subpath">
        <input value={subpath} placeholder="(repo root)" onChange={(event) => changeLocation(repoURL, event.target.value)} />
      </Field>
      {probe ? <ProbeNotice probe={probe} branch={source.branch || "main"} /> : null}
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <DialogActions
        busy={busy}
        saveLabel="Save location"
        saveDisabled={!verified || (repoURL.trim() === source.repo_url && subpath.trim() === source.subpath)}
        onClose={onClose}
        onSave={save}
        secondaryLabel="Probe repository"
        onSecondary={verify}
      />
    </Modal>
  );
}

function ReconnectCredentialDialog({ source, onClose, onChanged }: RepositoryDialogProps) {
  const { api, notify } = useApp();
  const [authMethod, setAuthMethod] = useState<GitAuthMethod>(source.creds_ref ? "pat" : "none");
  const [accessToken, setAccessToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [verified, setVerified] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function resetVerification() {
    setProbe(null);
    setVerified(false);
  }

  function credentialValue() {
    return gitCredentialSecretValue(authMethod, accessToken, username, password);
  }

  function probePayload() {
    const payload: Record<string, unknown> = {
      repo_url: source.repo_url,
      branch: source.branch || "main",
      subpath: source.subpath || undefined,
      auth_method: authMethod,
    };
    if (authMethod === "pat") payload.access_token = accessToken;
    if (authMethod === "basic") {
      payload.username = username;
      payload.password = password;
    }
    return payload;
  }

  async function verify() {
    if (authMethod !== "none" && !credentialValue()) {
      setError(authMethod === "pat" ? "Access token is required." : "Username and password are required.");
      return;
    }
    setBusy(true);
    setError("");
    setProbe(null);
    try {
      const result = await api.probeGitSource(probePayload());
      setProbe(result);
      setVerified(probePassed(result));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    setBusy(true);
    setError("");
    try {
      if (authMethod === "none") {
        await api.patchGitSource(source.id, { creds_ref: "" });
        notify("ok", "Repository access changed to public.");
      } else {
        const path = reconnectCredentialPath(source);
        await api.setVariable({
          path,
          value: credentialValue(),
          is_secret: true,
          description: `Git credential for source ${source.name}`,
        });
        await api.patchGitSource(source.id, { creds_ref: path });
        notify("ok", "Repository credential replaced.");
      }
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title="Reconnect repository access" subtitle={source.repo_url} onClose={onClose}>
      <Field label="Authentication">
        <select
          value={authMethod}
          onChange={(event) => {
            setAuthMethod(event.target.value as GitAuthMethod);
            resetVerification();
          }}
        >
          <option value="pat">Personal access token</option>
          <option value="basic">Username and password</option>
          <option value="none">Public repository</option>
        </select>
      </Field>
      {authMethod === "pat" ? (
        <Field label="Access token">
          <input
            type="password"
            autoComplete="new-password"
            value={accessToken}
            onChange={(event) => {
              setAccessToken(event.target.value);
              resetVerification();
            }}
          />
        </Field>
      ) : null}
      {authMethod === "basic" ? (
        <div className="formGrid two">
          <Field label="Username">
            <input
              autoComplete="username"
              value={username}
              onChange={(event) => {
                setUsername(event.target.value);
                resetVerification();
              }}
            />
          </Field>
          <Field label="Password or token">
            <input
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => {
                setPassword(event.target.value);
                resetVerification();
              }}
            />
          </Field>
        </div>
      ) : null}
      {probe ? <ProbeNotice probe={probe} branch={source.branch || "main"} /> : null}
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <DialogActions
        busy={busy}
        saveLabel="Save access"
        saveDisabled={!verified}
        onClose={onClose}
        onSave={save}
        secondaryLabel="Probe access"
        onSecondary={verify}
      />
    </Modal>
  );
}

type RepositoryDialogProps = {
  source: GitSource;
  onClose: () => void;
  onChanged: () => void;
};

function DialogActions({
  busy,
  saveLabel,
  saveDisabled,
  onClose,
  onSave,
  secondaryLabel,
  onSecondary,
}: {
  busy: boolean;
  saveLabel: string;
  saveDisabled: boolean;
  onClose: () => void;
  onSave: () => void;
  secondaryLabel?: string;
  onSecondary?: () => void;
}) {
  return (
    <footer className="dialogFooter">
      <span>
        {secondaryLabel && onSecondary ? (
          <button className="button" type="button" disabled={busy} onClick={onSecondary}>
            {busy ? "Checking…" : secondaryLabel}
          </button>
        ) : null}
      </span>
      <div className="dialogFooterActions">
        <button className="button" type="button" disabled={busy} onClick={onClose}>
          Cancel
        </button>
        <button className="button primary" type="button" disabled={busy || saveDisabled} onClick={onSave}>
          {busy ? "Saving…" : saveLabel}
        </button>
      </div>
    </footer>
  );
}
