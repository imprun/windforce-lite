const state = {
  workspace: localStorage.getItem("wf.workspace") || "default",
  token: localStorage.getItem("wf.token") || "",
  actor: localStorage.getItem("wf.actor") || "",
  credentialProfile: localStorage.getItem("wf.gitCredentialProfile") || "gitlab-default",
  variables: [],
  apps: [],
  appDetails: new Map(),
  selectedApp: "",
  selectedAction: "",
};

const credentialProfiles = [
  { id: "gitlab-default", label: "GitLab default", ref: "credentials/git/gitlab/default" },
  { id: "github-default", label: "GitHub default", ref: "credentials/git/github/default" },
  { id: "public", label: "Public repository", ref: "" },
  { id: "custom", label: "Custom profile", ref: "" },
];

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

function showNotice(message, type = "") {
  const notice = $("#notice");
  notice.textContent = message || "";
  notice.className = "notice" + (message ? " active" : "") + (type ? ` ${type}` : "");
}

function pretty(value) {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value === "string") {
    try {
      return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      return value;
    }
  }
  return JSON.stringify(value, null, 2);
}

function fmtDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

function short(value, size = 12) {
  const text = String(value || "");
  if (text.length <= size) return text || "-";
  return `${text.slice(0, size)}...`;
}

function pathPrefix() {
  return `/api/w/${encodeURIComponent(state.workspace)}`;
}

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  headers.set("accept", "application/json");
  if (state.token) headers.set("authorization", `Bearer ${state.token}`);
  if (state.actor) headers.set("x-windforce-actor", state.actor);
  let body = options.body;
  if (body !== undefined && !(body instanceof FormData) && typeof body !== "string") {
    headers.set("content-type", "application/json");
    body = JSON.stringify(body);
  }
  const response = await fetch(pathPrefix() + path, { ...options, headers, body });
  const text = await response.text();
  let data = text;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!response.ok) {
    const message = data && data.error ? data.error : `${response.status} ${response.statusText}`;
    throw new Error(message);
  }
  return data;
}

function bindNavigation() {
  $$(".nav-button").forEach((button) => {
    button.addEventListener("click", () => {
      activatePanel(button.dataset.panel);
    });
  });
  $$(".tab-button").forEach((button) => {
    button.addEventListener("click", () => {
      $$(".tab-button").forEach((item) => item.classList.toggle("active", item === button));
      $$(".tab-view").forEach((panel) => panel.classList.toggle("active", panel.id === button.dataset.tab));
    });
  });
}

function activatePanel(panelID) {
  $$(".nav-button").forEach((item) => item.classList.toggle("active", item.dataset.panel === panelID));
  $$(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === panelID));
}

function readSettings() {
  $("#workspaceInput").value = state.workspace;
  $("#tokenInput").value = state.token;
  $("#actorInput").value = state.actor;
  renderCredentialProfileOptions();
  updateContextBadges();
  updateCredentialControls();
}

function saveSettings() {
  state.workspace = $("#workspaceInput").value.trim() || "default";
  state.token = $("#tokenInput").value.trim();
  state.actor = $("#actorInput").value.trim();
  localStorage.setItem("wf.workspace", state.workspace);
  localStorage.setItem("wf.token", state.token);
  localStorage.setItem("wf.actor", state.actor);
  updateContextBadges();
}

function updateContextBadges() {
  $("#currentWorkspace").textContent = state.workspace || "default";
  $("#currentActor").textContent = state.actor || "system";
}

function renderCredentialProfileOptions() {
  const select = $("#sourceAuthProfile");
  select.innerHTML = credentialProfiles.map((profile) => `<option value="${escapeAttr(profile.id)}">${escapeHTML(profile.label)}</option>`).join("");
  if (!credentialProfiles.some((profile) => profile.id === state.credentialProfile)) {
    state.credentialProfile = credentialProfiles[0].id;
  }
  select.value = state.credentialProfile;
}

function selectedCredentialProfile() {
  const id = $("#sourceAuthProfile").value || credentialProfiles[0].id;
  return credentialProfiles.find((profile) => profile.id === id) || credentialProfiles[0];
}

function resolveCredentialRef() {
  const profile = selectedCredentialProfile();
  if (profile.id === "public") return "";
  if (profile.id === "custom") return $("#sourceCreds").value.trim();
  return profile.ref;
}

function credentialVariable(ref) {
  return state.variables.find((variable) => variable.path === ref && !variable.app_key);
}

function credentialLabelForRef(ref) {
  if (!ref) return "Public repository";
  const profile = credentialProfiles.find((item) => item.ref === ref);
  return profile ? profile.label : `Custom profile (${ref})`;
}

function updateCredentialControls() {
  const profile = selectedCredentialProfile();
  const isPublic = profile.id === "public";
  const isCustom = profile.id === "custom";
  const ref = resolveCredentialRef();
  $("#sourceCredsLabel").hidden = !isCustom;
  $("#sourceCredentialValueLabel").hidden = isPublic;
  $("#sourceCreds").required = isCustom;
  $("#sourceCredsPreview").textContent = ref || "no credentials";

  const status = $("#sourceCredsStatus");
  if (isPublic) {
    status.textContent = "public";
    status.className = "pill muted";
    return;
  }
  if (!ref) {
    status.textContent = "path required";
    status.className = "pill bad";
    return;
  }
  const variable = credentialVariable(ref);
  status.textContent = variable ? (variable.is_secret ? "secret stored" : "value stored") : "not stored";
  status.className = `pill ${variable ? "ok" : "warn"}`;
}

function renderCredentialProfiles() {
  const rows = credentialProfiles
    .filter((profile) => profile.id !== "custom")
    .map((profile) => {
      const variable = profile.ref ? credentialVariable(profile.ref) : null;
      const status = profile.ref ? (variable ? "stored" : "not stored") : "public";
      const pill = profile.ref ? (variable ? "ok" : "warn") : "muted";
      return `
        <div class="list-row profile-row" data-profile="${escapeAttr(profile.id)}">
          <div>
            <div class="row-title">${escapeHTML(profile.label)}</div>
            <div class="row-meta">${escapeHTML(profile.ref || "no credential reference")}</div>
          </div>
          <span class="pill ${pill}">${escapeHTML(status)}</span>
        </div>`;
    })
    .join("");
  $("#credentialProfiles").innerHTML = rows || '<div class="row-meta">No credential profiles.</div>';
  $$("#credentialProfiles [data-profile]").forEach((row) => {
    row.addEventListener("click", () => {
      state.credentialProfile = row.dataset.profile;
      $("#sourceAuthProfile").value = state.credentialProfile;
      localStorage.setItem("wf.gitCredentialProfile", state.credentialProfile);
      updateCredentialControls();
    });
  });
}

async function refreshAll() {
  showNotice("Refreshing...");
  try {
    const results = await Promise.allSettled([loadVariables(), loadOverview(), loadSources(), loadApps()]);
    const failed = results.find((result) => result.status === "rejected");
    if (failed) throw failed.reason;
    $("#apiStatus").textContent = "ready";
    $("#apiStatus").className = "pill ok";
    showNotice("Refreshed", "ok");
    setTimeout(() => showNotice(""), 1600);
  } catch (error) {
    $("#apiStatus").textContent = "error";
    $("#apiStatus").className = "pill bad";
    showNotice(error.message, "error");
  }
}

async function loadVariables() {
  state.variables = await api("/variables");
  renderCredentialProfiles();
  updateCredentialControls();
}

async function loadOverview() {
  try {
    const [sources, appsResponse, tags] = await Promise.all([
      api("/git_sources"),
      api("/apps?view=summary"),
      api("/worker-tags"),
    ]);
    const apps = Array.isArray(appsResponse) ? appsResponse : appsResponse.apps || [];
    const workerTags = tags && Array.isArray(tags.tags) ? tags.tags : [];
    $("#appCount").textContent = apps.length;
    $("#sourceCount").textContent = sources.length;
    $("#actionCount").textContent = apps.reduce((total, app) => total + Number(app.actions_count || 0), 0);
    $("#workerTagCount").textContent = workerTags.length;
    renderWorkerTags(tags);
    renderActiveDeployments(apps);
  } catch (error) {
    $("#apiStatus").textContent = "error";
    $("#apiStatus").className = "pill bad";
    renderWorkerTags({ tags: [] });
    $("#activeDeployments").innerHTML = `<div class="row-meta">${escapeHTML(error.message)}</div>`;
    throw error;
  }
}

function renderWorkerTags(payload) {
  const tags = payload && Array.isArray(payload.tags) ? payload.tags : [];
  $("#workerTags").innerHTML =
    tags.length === 0
      ? '<div class="row-meta">No worker tags reported.</div>'
      : tags
          .map(
            (tag) => `
            <div class="tag-row">
              <div>
                <span class="row-title">${escapeHTML(tag.tag || "default")}</span>
                <span class="pill ${tag.live_workers > 0 ? "ok" : "muted"}">${tag.live_workers || 0} workers</span>
              </div>
              <div class="row-meta">${escapeHTML((tag.capabilities || []).join(", ") || "no capability labels")}</div>
            </div>`,
          )
          .join("");
}

function renderActiveDeployments(apps) {
  $("#activeDeployments").innerHTML =
    apps.length === 0
      ? '<div class="row-meta">No apps deployed.</div>'
      : apps
          .map(
            (app) => `
            <div class="list-row" data-app="${escapeAttr(app.app_key || "")}">
              <div class="row-title">${escapeHTML(app.app_key || "-")}</div>
              <div><span class="pill ok">active</span></div>
              <div class="row-meta">${escapeHTML(app.script_lang || "unknown")} · ${escapeHTML(short(app.commit_sha, 14))} · ${escapeHTML(fmtDate(app.updated_at))}</div>
            </div>`,
          )
          .join("");
  $$("#activeDeployments .list-row").forEach((row) => row.addEventListener("click", async () => {
    activatePanel("appsPanel");
    await selectApp(row.dataset.app);
  }));
}

async function loadSources() {
  const sources = await api("/git_sources");
  $("#sourceList").innerHTML =
    sources.length === 0
      ? '<div class="row-meta">No deployable apps registered.</div>'
      : sources
          .map(
            (source) => `
            <div class="table-row">
              <div>
                <div class="row-title">${escapeHTML(source.name)}</div>
                <div class="row-meta">${escapeHTML(source.repo_url)}</div>
              </div>
              <div>
                <span class="pill muted">source</span>
                <span class="pill">${escapeHTML(source.branch || "main")}</span>
              </div>
              <div class="row-meta">
                ${escapeHTML(source.subpath || "root")}<br />
                deployed ${escapeHTML(short(source.last_synced_commit, 14))}<br />
                ${escapeHTML(credentialLabelForRef(source.creds_ref || ""))}
              </div>
              <div class="form-actions">
                <button class="button primary" data-sync-id="${escapeAttr(source.id)}" type="button">Deploy</button>
                <button class="button danger" data-delete-id="${escapeAttr(source.id)}" type="button">Remove</button>
              </div>
            </div>`,
          )
          .join("");
  $$("[data-sync-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      await runAction(`Deploying app source ${button.dataset.syncId}`, async () => {
        const result = await api(`/git_sources/${encodeURIComponent(button.dataset.syncId)}/sync`, { method: "POST" });
        state.appDetails.clear();
        showNotice(`Deployed ${result.app || "app"} at ${short(result.commit, 12)}`, "ok");
        await Promise.all([loadSources(), loadApps()]);
      }, false);
    });
  });
  $$("[data-delete-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      if (!confirm(`Remove app source ${button.dataset.deleteId}?`)) return;
      await runAction("Removing app source", async () => {
        await api(`/git_sources/${encodeURIComponent(button.dataset.deleteId)}`, { method: "DELETE" });
        state.appDetails.clear();
        await Promise.all([loadSources(), loadApps()]);
      });
    });
  });
}

async function registerSource(event) {
  event.preventDefault();
  const credsRef = resolveCredentialRef();
  const payload = {
    name: $("#sourceName").value.trim(),
    repo_url: $("#sourceRepo").value.trim(),
    branch: $("#sourceBranch").value.trim(),
    subpath: $("#sourceSubpath").value.trim(),
    creds_ref: credsRef,
  };
  await runAction("Registering app source", async () => {
    await saveCredentialIfProvided(credsRef);
    await api("/git_sources", { method: "POST", body: payload });
    event.target.reset();
    $("#sourceAuthProfile").value = state.credentialProfile;
    $("#sourceCredentialValue").value = "";
    updateCredentialControls();
    await Promise.all([loadSources(), loadApps()]);
  });
}

async function probeSource() {
  const repoURL = $("#sourceRepo").value.trim();
  if (!repoURL) {
    showNotice("Repository URL is required for probe.", "error");
    return;
  }
  await runAction("Probing app source", async () => {
    const credsRef = resolveCredentialRef();
    await saveCredentialIfProvided(credsRef);
    const result = await api("/git_sources/probe", {
      method: "POST",
      body: {
        repo_url: repoURL,
        branch: $("#sourceBranch").value.trim(),
        creds_ref: credsRef,
      },
    });
    showNotice(result.reachable ? "Repository reachable for deployment." : result.error || "Repository is not reachable.", result.reachable ? "ok" : "error");
  });
}

async function saveCredentialIfProvided(credsRef) {
  const tokenInput = $("#sourceCredentialValue");
  const value = tokenInput.value.trim();
  if (!value) return;
  if (!credsRef) {
    throw new Error("Credential profile path is required.");
  }
  await api("/variables", {
    method: "POST",
    body: {
      path: credsRef,
      value,
      is_secret: true,
      description: `Git credential for ${credentialLabelForRef(credsRef)}`,
    },
  });
  tokenInput.value = "";
  await loadVariables();
}

async function createSampleSource() {
  const appKey = prompt("Sample app key", "echo");
  if (!appKey) return;
  await runAction("Creating sample app", async () => {
    await api("/git_sources/sample", { method: "POST", body: { app_key: appKey.trim() } });
    state.appDetails.clear();
    await Promise.all([loadSources(), loadApps()]);
  });
}

async function loadApps() {
  const response = await api("/apps?view=summary");
  state.apps = Array.isArray(response) ? response : response.apps || [];
  if (!state.selectedApp && state.apps.length > 0) state.selectedApp = state.apps[0].app_key || state.apps[0];
  renderApps();
  if (state.selectedApp) await selectApp(state.selectedApp, false);
}

function renderApps() {
  $("#appList").innerHTML =
    state.apps.length === 0
      ? '<div class="row-meta">No apps deployed yet.</div>'
      : state.apps
          .map((app) => {
            const appKey = app.app_key || app;
            return `
              <div class="list-row ${state.selectedApp === appKey ? "selected" : ""}" data-app="${escapeAttr(appKey)}">
                <div class="row-title">${escapeHTML(appKey)}</div>
                <div class="row-meta">${escapeHTML(app.script_lang || "unknown")} · deployed ${escapeHTML(short(app.commit_sha, 12))}</div>
                <div><span class="pill">${escapeHTML(app.effective_route_tag || app.tag || "default")}</span></div>
              </div>`;
          })
          .join("");
  $$("#appList .list-row").forEach((row) => row.addEventListener("click", () => selectApp(row.dataset.app)));
}

async function selectApp(appKey, rerender = true) {
  state.selectedApp = appKey;
  if (rerender) renderApps();
  let detail = state.appDetails.get(appKey);
  if (!detail) {
    detail = await api(`/apps/${encodeURIComponent(appKey)}`);
    state.appDetails.set(appKey, detail);
  }
  renderAppDetail(detail);
  if (!state.selectedAction && detail.actions && detail.actions.length > 0) state.selectedAction = detail.actions[0].action_key;
  if (state.selectedAction) await selectAction(state.selectedAction, false);
}

function renderAppDetail(detail) {
  const app = detail.app || {};
  $("#appDetail").innerHTML = `
    <div class="detail-grid">
      ${kv("Deployed app", app.app_key)}
      ${kv("Active commit", short(app.commit_sha, 16))}
      ${kv("Entrypoint", app.entrypoint)}
      ${kv("Language", app.script_lang)}
      ${kv("Route tag", app.effective_route_tag || app.tag)}
      ${kv("Source ref", app.git_source_id)}
      ${kv("Updated", fmtDate(app.updated_at))}
    </div>`;
  $("#actionList").innerHTML =
    (detail.actions || [])
      .map(
        (action) => `
        <button class="action-button ${state.selectedAction === action.action_key ? "selected" : ""}" data-action="${escapeAttr(action.action_key)}" type="button">
          <span class="row-title">${escapeHTML(action.action_key)}</span>
          <span class="row-meta">${escapeHTML(action.effective_route_tag || action.tag || "default")} · ${escapeHTML((action.effective_capabilities || []).join(", ") || "no capabilities")}</span>
        </button>`,
      )
      .join("") || '<div class="row-meta">No actions.</div>';
  $$("#actionList [data-action]").forEach((button) => button.addEventListener("click", () => selectAction(button.dataset.action)));
}

function kv(label, value) {
  return `<div class="kv"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value || "-")}</strong></div>`;
}

async function selectAction(actionKey, rerender = true) {
  state.selectedAction = actionKey;
  if (rerender && state.selectedApp) renderAppDetail(state.appDetails.get(state.selectedApp) || {});
  await Promise.all([loadActionSchema(), loadAppHistory(), loadAppSource()]);
}

async function loadActionSchema() {
  if (!state.selectedApp || !state.selectedAction) return;
  const schema = await api(`/apps/${encodeURIComponent(state.selectedApp)}/actions/${encodeURIComponent(state.selectedAction)}/schema`);
  $("#schemaTab").textContent = pretty(schema);
}

async function loadAppHistory() {
  if (!state.selectedApp) return;
  const history = await api(`/apps/${encodeURIComponent(state.selectedApp)}/history`);
  $("#historyTab").innerHTML =
    history.length === 0
      ? '<div class="row-meta">No deployments yet.</div>'
      : history
          .map(
            (item) => `
            <div class="list-row">
              <div class="row-title">Deployment ${escapeHTML(short(item.id, 10))}</div>
              <div><span class="pill ok">${escapeHTML(short(item.commit_sha, 12))}</span></div>
              <div class="row-meta">${escapeHTML(item.source || "-")} · ${escapeHTML(fmtDate(item.created_at))}</div>
              <div class="row-meta">${escapeHTML(item.message || "")}</div>
            </div>`,
          )
          .join("");
}

async function loadAppSource() {
  if (!state.selectedApp) return;
  try {
    const source = await api(`/apps/${encodeURIComponent(state.selectedApp)}/source`);
    const files = source.files || {};
    $("#sourceTab").textContent = Object.keys(files)
      .sort()
      .map((name) => `# ${name}\n${files[name]}`)
      .join("\n\n");
  } catch (error) {
    $("#sourceTab").textContent = error.message;
  }
}

async function showAppOpenAPI() {
  if (!state.selectedApp) {
    showNotice("Select an app first.", "error");
    return;
  }
  await runAction("Loading app OpenAPI", async () => {
    const spec = await api(`/apps/${encodeURIComponent(state.selectedApp)}/openapi.json`);
    $("#schemaTab").textContent = pretty(spec);
    activateTab("schemaTab");
  });
}

function activateTab(id) {
  $$(".tab-button").forEach((button) => button.classList.toggle("active", button.dataset.tab === id));
  $$(".tab-view").forEach((panel) => panel.classList.toggle("active", panel.id === id));
}

async function runAction(message, fn, showSuccess = true) {
  showNotice(message);
  try {
    const result = await fn();
    if (showSuccess) {
      showNotice("Done", "ok");
      setTimeout(() => showNotice(""), 1400);
    }
    return result;
  } catch (error) {
    showNotice(error.message, "error");
    throw error;
  }
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function escapeAttr(value) {
  return escapeHTML(value);
}

function bindForms() {
  $("#settingsButton").addEventListener("click", () => {
    const dialog = $("#settingsDialog");
    if (dialog.showModal) dialog.showModal();
    else dialog.setAttribute("open", "");
  });
  $("#settingsCloseButton").addEventListener("click", () => {
    const dialog = $("#settingsDialog");
    if (dialog.close) dialog.close();
    else dialog.removeAttribute("open");
  });
  $("#settingsForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    saveSettings();
    const dialog = $("#settingsDialog");
    if (dialog.close) dialog.close();
    else dialog.removeAttribute("open");
    state.appDetails.clear();
    state.selectedApp = "";
    state.selectedAction = "";
    await refreshAll();
  });
  $("#refreshButton").addEventListener("click", refreshAll);
  $("#sourceAuthProfile").addEventListener("change", () => {
    state.credentialProfile = $("#sourceAuthProfile").value;
    localStorage.setItem("wf.gitCredentialProfile", state.credentialProfile);
    updateCredentialControls();
  });
  $("#sourceCreds").addEventListener("input", updateCredentialControls);
  $("#sourceForm").addEventListener("submit", registerSource);
  $("#probeSourceButton").addEventListener("click", probeSource);
  $("#sampleSourceButton").addEventListener("click", createSampleSource);
  $("#openApiButton").addEventListener("click", showAppOpenAPI);
}

readSettings();
bindNavigation();
bindForms();
refreshAll();
