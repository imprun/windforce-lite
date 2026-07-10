const state = {
  workspace: localStorage.getItem("wf.workspace") || "default",
  token: localStorage.getItem("wf.token") || "",
  actor: localStorage.getItem("wf.actor") || "",
  apps: [],
  appDetails: new Map(),
  selectedApp: "",
  selectedAction: "",
  selectedJob: "",
};

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

function statusClass(status) {
  const value = String(status || "").toLowerCase();
  if (value === "success" || value === "succeeded" || value === "completed") return "ok";
  if (value === "running" || value === "queued" || value === "pending") return "warn";
  if (value === "failure" || value === "failed" || value === "canceled") return "bad";
  return "muted";
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
      $$(".nav-button").forEach((item) => item.classList.toggle("active", item === button));
      $$(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === button.dataset.panel));
    });
  });
  $$(".tab-button").forEach((button) => {
    button.addEventListener("click", () => {
      $$(".tab-button").forEach((item) => item.classList.toggle("active", item === button));
      $$(".tab-view").forEach((panel) => panel.classList.toggle("active", panel.id === button.dataset.tab));
    });
  });
}

function readSettings() {
  $("#workspaceInput").value = state.workspace;
  $("#tokenInput").value = state.token;
  $("#actorInput").value = state.actor;
}

function saveSettings() {
  state.workspace = $("#workspaceInput").value.trim() || "default";
  state.token = $("#tokenInput").value.trim();
  state.actor = $("#actorInput").value.trim();
  localStorage.setItem("wf.workspace", state.workspace);
  localStorage.setItem("wf.token", state.token);
  localStorage.setItem("wf.actor", state.actor);
}

async function refreshAll() {
  showNotice("Refreshing...");
  try {
    const results = await Promise.allSettled([loadOverview(), loadSources(), loadApps(), loadJobs()]);
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

async function loadOverview() {
  try {
    const [summary, tags, jobs] = await Promise.all([
      api("/jobs/summary"),
      api("/worker-tags"),
      api("/jobs?limit=5"),
    ]);
    $("#queuedCount").textContent = summary.queued_count || 0;
    $("#runningCount").textContent = summary.running_count || 0;
    $("#completedCount").textContent = summary.completed_count_recent || 0;
    $("#failedCount").textContent = summary.failed_count_recent || 0;
    renderWorkerTags(tags);
    renderRecentJobs(jobs.items || []);
  } catch (error) {
    $("#apiStatus").textContent = "error";
    $("#apiStatus").className = "pill bad";
    renderWorkerTags({ tags: [] });
    $("#recentJobs").innerHTML = `<div class="row-meta">${escapeHTML(error.message)}</div>`;
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

function renderRecentJobs(jobs) {
  $("#recentJobs").innerHTML =
    jobs.length === 0
      ? '<div class="row-meta">No jobs yet.</div>'
      : jobs
          .map(
            (job) => `
            <div class="list-row" data-job-id="${escapeAttr(job.id)}">
              <div class="row-title">${escapeHTML(job.app_key || "-")}.${escapeHTML(job.action_key || "-")}</div>
              <div><span class="pill ${statusClass(job.status)}">${escapeHTML(job.status || "unknown")}</span></div>
              <div class="row-meta">${escapeHTML(short(job.id, 18))} · ${escapeHTML(fmtDate(job.created_at))}</div>
            </div>`,
          )
          .join("");
  $$("#recentJobs .list-row").forEach((row) => row.addEventListener("click", () => selectJob(row.dataset.jobId)));
}

async function loadSources() {
  const sources = await api("/git_sources");
  $("#sourceList").innerHTML =
    sources.length === 0
      ? '<div class="row-meta">No git sources registered.</div>'
      : sources
          .map(
            (source) => `
            <div class="table-row">
              <div>
                <div class="row-title">${escapeHTML(source.name)}</div>
                <div class="row-meta">${escapeHTML(source.repo_url)}</div>
              </div>
              <div>
                <span class="pill muted">#${escapeHTML(source.id)}</span>
                <span class="pill">${escapeHTML(source.branch || "main")}</span>
              </div>
              <div class="row-meta">
                ${escapeHTML(source.subpath || "root")}<br />
                ${escapeHTML(short(source.last_synced_commit, 14))}
              </div>
              <div class="form-actions">
                <button class="button" data-sync-id="${escapeAttr(source.id)}" type="button">Sync</button>
                <button class="button danger" data-delete-id="${escapeAttr(source.id)}" type="button">Delete</button>
              </div>
            </div>`,
          )
          .join("");
  $$("[data-sync-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      await runAction(`Syncing source ${button.dataset.syncId}`, async () => {
        const result = await api(`/git_sources/${encodeURIComponent(button.dataset.syncId)}/sync`, { method: "POST" });
        showNotice(`Synced ${result.app || "source"} at ${short(result.commit, 12)}`, "ok");
        await Promise.all([loadSources(), loadApps()]);
      });
    });
  });
  $$("[data-delete-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      if (!confirm(`Delete git source ${button.dataset.deleteId}?`)) return;
      await runAction("Deleting source", async () => {
        await api(`/git_sources/${encodeURIComponent(button.dataset.deleteId)}`, { method: "DELETE" });
        await Promise.all([loadSources(), loadApps()]);
      });
    });
  });
}

async function registerSource(event) {
  event.preventDefault();
  const payload = {
    name: $("#sourceName").value.trim(),
    repo_url: $("#sourceRepo").value.trim(),
    branch: $("#sourceBranch").value.trim(),
    subpath: $("#sourceSubpath").value.trim(),
    creds_ref: $("#sourceCreds").value.trim(),
  };
  await runAction("Registering source", async () => {
    await api("/git_sources", { method: "POST", body: payload });
    event.target.reset();
    await loadSources();
  });
}

async function probeSource() {
  const repoURL = $("#sourceRepo").value.trim();
  if (!repoURL) {
    showNotice("Repository URL is required for probe.", "error");
    return;
  }
  await runAction("Probing source", async () => {
    const result = await api("/git_sources/probe", {
      method: "POST",
      body: {
        repo_url: repoURL,
        branch: $("#sourceBranch").value.trim(),
        creds_ref: $("#sourceCreds").value.trim(),
      },
    });
    showNotice(result.reachable ? "Repository reachable." : result.error || "Repository is not reachable.", result.reachable ? "ok" : "error");
  });
}

async function createSampleSource() {
  const appKey = prompt("Sample app key", "echo");
  if (!appKey) return;
  await runAction("Creating sample source", async () => {
    await api("/git_sources/sample", { method: "POST", body: { app_key: appKey.trim() } });
    await Promise.all([loadSources(), loadApps()]);
  });
}

async function loadApps() {
  const response = await api("/apps?view=summary");
  state.apps = Array.isArray(response) ? response : response.apps || [];
  if (!state.selectedApp && state.apps.length > 0) state.selectedApp = state.apps[0].app_key || state.apps[0];
  renderApps();
  fillRunAppSelect();
  if (state.selectedApp) await selectApp(state.selectedApp, false);
}

function renderApps() {
  $("#appList").innerHTML =
    state.apps.length === 0
      ? '<div class="row-meta">No apps synced yet.</div>'
      : state.apps
          .map((app) => {
            const appKey = app.app_key || app;
            return `
              <div class="list-row ${state.selectedApp === appKey ? "selected" : ""}" data-app="${escapeAttr(appKey)}">
                <div class="row-title">${escapeHTML(appKey)}</div>
                <div class="row-meta">${escapeHTML(app.script_lang || "unknown")} · ${escapeHTML(short(app.commit_sha, 12))}</div>
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
  fillRunActionSelect(detail.actions || []);
  if (!state.selectedAction && detail.actions && detail.actions.length > 0) state.selectedAction = detail.actions[0].action_key;
  if (state.selectedAction) await selectAction(state.selectedAction, false);
}

function renderAppDetail(detail) {
  const app = detail.app || {};
  $("#appDetail").innerHTML = `
    <div class="detail-grid">
      ${kv("App", app.app_key)}
      ${kv("Commit", short(app.commit_sha, 16))}
      ${kv("Entrypoint", app.entrypoint)}
      ${kv("Language", app.script_lang)}
      ${kv("Route tag", app.effective_route_tag || app.tag)}
      ${kv("Git source", app.git_source_id)}
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
  fillRunActionSelect((state.appDetails.get(state.selectedApp) || {}).actions || []);
  $("#runAction").value = actionKey;
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
      ? '<div class="row-meta">No deployment history.</div>'
      : history
          .map(
            (item) => `
            <div class="list-row">
              <div class="row-title">${escapeHTML(short(item.commit_sha, 16))}</div>
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

function fillRunAppSelect() {
  const select = $("#runApp");
  select.innerHTML = state.apps
    .map((app) => {
      const key = app.app_key || app;
      return `<option value="${escapeAttr(key)}">${escapeHTML(key)}</option>`;
    })
    .join("");
  if (state.selectedApp) select.value = state.selectedApp;
}

function fillRunActionSelect(actions) {
  const select = $("#runAction");
  select.innerHTML = actions
    .map((action) => `<option value="${escapeAttr(action.action_key)}">${escapeHTML(action.action_key)}</option>`)
    .join("");
  if (state.selectedAction) select.value = state.selectedAction;
}

async function runJob(event) {
  event.preventDefault();
  const app = $("#runApp").value;
  const action = $("#runAction").value;
  if (!app || !action) {
    showNotice("Select an app and action.", "error");
    return;
  }
  let input;
  try {
    input = JSON.parse($("#runInput").value || "{}");
  } catch (error) {
    showNotice(`Input JSON is invalid: ${error.message}`, "error");
    return;
  }
  const wait = $("#runWait").checked;
  const timeout = Number($("#runTimeout").value || 20000);
  const suffix = wait ? `/wait?timeout_ms=${encodeURIComponent(timeout)}` : "";
  await runAction("Running job", async () => {
    const result = await api(`/jobs/run/${encodeURIComponent(app)}/${encodeURIComponent(action)}${suffix}`, {
      method: "POST",
      body: input,
    });
    $("#jobResult").textContent = pretty(result);
    state.selectedJob = result.job_id || state.selectedJob;
    await loadJobs();
  });
}

async function loadJobs() {
  const params = new URLSearchParams();
  params.set("limit", $("#jobLimit").value || "20");
  const status = $("#jobStatusFilter").value;
  const app = $("#jobAppFilter").value.trim();
  if (status) params.set("status", status);
  if (app) params.set("app", app);
  const jobs = await api(`/jobs?${params.toString()}`);
  renderJobs(jobs.items || []);
}

function renderJobs(jobs) {
  $("#jobList").innerHTML =
    jobs.length === 0
      ? '<div class="row-meta">No jobs match the current filter.</div>'
      : jobs
          .map(
            (job) => `
            <div class="table-row ${state.selectedJob === job.id ? "selected" : ""}">
              <div>
                <div class="row-title">${escapeHTML(job.app_key || "-")}.${escapeHTML(job.action_key || "-")}</div>
                <div class="row-meta">${escapeHTML(job.id)}</div>
              </div>
              <div><span class="pill ${statusClass(job.status)}">${escapeHTML(job.status || "unknown")}</span></div>
              <div class="row-meta">${escapeHTML(job.trigger_kind || "-")} · ${escapeHTML(fmtDate(job.created_at))}</div>
              <div class="form-actions">
                <button class="button" data-job-open="${escapeAttr(job.id)}" type="button">Open</button>
                <button class="button" data-job-logs="${escapeAttr(job.id)}" type="button">Logs</button>
              </div>
            </div>`,
          )
          .join("");
  $$("[data-job-open]").forEach((button) => button.addEventListener("click", () => selectJob(button.dataset.jobOpen)));
  $$("[data-job-logs]").forEach((button) => button.addEventListener("click", () => loadJobLogs(button.dataset.jobLogs)));
}

async function selectJob(jobID) {
  if (!jobID) return;
  state.selectedJob = jobID;
  await runAction("Loading job", async () => {
    const [status, result] = await Promise.allSettled([
      api(`/jobs/${encodeURIComponent(jobID)}`),
      api(`/jobs/${encodeURIComponent(jobID)}/result`),
    ]);
    $("#jobResult").textContent = pretty({
      status: status.status === "fulfilled" ? status.value : status.reason.message,
      result: result.status === "fulfilled" ? result.value : result.reason.message,
    });
    await loadJobLogs(jobID);
  }, false);
}

async function loadJobLogs(jobID = state.selectedJob) {
  if (!jobID) return;
  try {
    const logs = await api(`/jobs/${encodeURIComponent(jobID)}/logs?tail_bytes=1048576`);
    $("#jobLogs").textContent = typeof logs === "string" ? logs : pretty(logs);
  } catch (error) {
    $("#jobLogs").textContent = error.message;
  }
}

async function cancelSelectedJob() {
  if (!state.selectedJob) {
    showNotice("Select a job first.", "error");
    return;
  }
  if (!confirm(`Cancel job ${state.selectedJob}?`)) return;
  await runAction("Canceling job", async () => {
    const result = await api(`/jobs/${encodeURIComponent(state.selectedJob)}/cancel`, {
      method: "POST",
      body: { reason: "canceled from windforce-lite UI" },
    });
    $("#jobResult").textContent = pretty(result);
    await loadJobs();
  });
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

function formatInput() {
  try {
    $("#runInput").value = JSON.stringify(JSON.parse($("#runInput").value || "{}"), null, 2);
  } catch (error) {
    showNotice(`Input JSON is invalid: ${error.message}`, "error");
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
  $("#settingsForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    saveSettings();
    state.appDetails.clear();
    state.selectedApp = "";
    state.selectedAction = "";
    await refreshAll();
  });
  $("#refreshButton").addEventListener("click", refreshAll);
  $("#sourceForm").addEventListener("submit", registerSource);
  $("#probeSourceButton").addEventListener("click", probeSource);
  $("#sampleSourceButton").addEventListener("click", createSampleSource);
  $("#openApiButton").addEventListener("click", showAppOpenAPI);
  $("#runForm").addEventListener("submit", runJob);
  $("#formatInputButton").addEventListener("click", formatInput);
  $("#reloadJobsButton").addEventListener("click", loadJobs);
  $("#cancelJobButton").addEventListener("click", cancelSelectedJob);
  $("#jobStatusFilter").addEventListener("change", loadJobs);
  $("#jobAppFilter").addEventListener("change", loadJobs);
  $("#jobLimit").addEventListener("change", loadJobs);
  $("#runApp").addEventListener("change", async (event) => {
    state.selectedAction = "";
    await selectApp(event.target.value);
  });
  $("#runAction").addEventListener("change", async (event) => {
    await selectAction(event.target.value);
  });
}

readSettings();
bindNavigation();
bindForms();
refreshAll();
