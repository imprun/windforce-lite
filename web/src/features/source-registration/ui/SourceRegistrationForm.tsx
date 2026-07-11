"use client";

import { useMemo, useState } from "react";
import { compactRecord } from "@/shared/lib/format";

type Props = {
  busy: boolean;
  onRegister: (payload: Record<string, unknown>) => Promise<void>;
  onProbe: (payload: Record<string, unknown>) => Promise<void>;
  onCreateSample: () => Promise<void>;
  onCancel: () => void;
};

type AuthMethod = "none" | "pat" | "basic";

export function SourceRegistrationForm({ busy, onRegister, onProbe, onCreateSample, onCancel }: Props) {
  const [name, setName] = useState("");
  const [repoURL, setRepoURL] = useState("");
  const [branch, setBranch] = useState("main");
  const [subpath, setSubpath] = useState("");
  const [authMethod, setAuthMethod] = useState<AuthMethod>("none");
  const [accessToken, setAccessToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const credentialPath = useMemo(() => `git/${slug(name)}/credential`, [name]);

  function payload() {
    return compactRecord({
      name,
      repo_url: repoURL,
      branch,
      subpath,
      auth_method: authMethod === "none" ? "" : authMethod,
      access_token: authMethod === "pat" ? accessToken : "",
      username: authMethod === "basic" ? username : "",
      password: authMethod === "basic" ? password : "",
    });
  }

  return (
    <form
      id="sourceRegistration"
      className="registrationForm"
      onSubmit={(event) => {
        event.preventDefault();
        void onRegister(payload());
      }}
    >
      <header className="dialogHeader">
        <div>
          <span className="eyebrow">Source registry</span>
          <h2>Register App Source</h2>
          <p>Repository access, branch, manifest, schemas, and lockfile are validated before saving.</p>
        </div>
        <div className="actions">
          <button className="button" type="button" onClick={onCreateSample} disabled={busy}>
            Create Sample
          </button>
          <button className="button" type="button" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </header>

      <div className="registrationGrid">
        <label className="field">
          Source name
          <input id="sourceName" value={name} onChange={(event) => setName(event.target.value)} required placeholder="4MDCPCM" spellCheck={false} />
        </label>
        <label className="field span2">
          Repository URL
          <input id="sourceRepo" value={repoURL} onChange={(event) => setRepoURL(event.target.value)} required placeholder="https://gitlab.example.com/group/repo.git" spellCheck={false} />
        </label>
        <label className="field">
          Branch
          <input id="sourceBranch" value={branch} onChange={(event) => setBranch(event.target.value)} spellCheck={false} />
        </label>
        <label className="field">
          Subpath
          <input id="sourceSubpath" value={subpath} onChange={(event) => setSubpath(event.target.value)} placeholder="apps/coupang-eats" spellCheck={false} />
        </label>
        <label className="field">
          Git authentication
          <select id="sourceAuthMethod" value={authMethod} onChange={(event) => setAuthMethod(event.target.value as AuthMethod)}>
            <option value="none">No authentication</option>
            <option value="pat">Personal access token</option>
            <option value="basic">Username / password</option>
          </select>
        </label>
        <div className="credentialPreview">
          <span>Credential storage</span>
          <strong>{authMethod === "none" ? "public repository" : credentialPath}</strong>
        </div>
        {authMethod === "pat" ? (
          <label className="field span2">
            Personal access token
            <input type="password" value={accessToken} onChange={(event) => setAccessToken(event.target.value)} />
          </label>
        ) : null}
        {authMethod === "basic" ? (
          <>
            <label className="field">
              Username
              <input value={username} onChange={(event) => setUsername(event.target.value)} />
            </label>
            <label className="field">
              Password or token
              <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} />
            </label>
          </>
        ) : null}
      </div>

      <div className="dialogFooter">
        <button className="button" type="button" onClick={() => void onProbe(payload())} disabled={busy}>
          Probe Git
        </button>
        <button className="button primary" type="submit" disabled={busy}>
          Register Source
        </button>
      </div>
    </form>
  );
}

function slug(value: string): string {
  return value.toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "") || "source";
}
