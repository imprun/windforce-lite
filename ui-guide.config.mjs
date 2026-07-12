import { spawn } from "node:child_process";
import { mkdir, rm } from "node:fs/promises";
import path from "node:path";

const port = Number(process.env.WINDFORCE_LITE_UI_GUIDE_PORT || 18099);
const baseDir = path.resolve(".tmp/ui-guide");
const binary = path.resolve(
  baseDir,
  process.platform === "win32" ? "windforce-lite.exe" : "windforce-lite",
);

let server = null;

function stopServer() {
  if (server && server.exitCode === null) server.kill();
  server = null;
}

export default {
  name: "windforce-lite",
  baseUrl: process.env.WINDFORCE_LITE_UI_URL || `http://127.0.0.1:${port}/ui/`,
  apiBaseUrl: process.env.WINDFORCE_LITE_API_URL || `http://127.0.0.1:${port}/api/w/default`,
  guidePath: "docs/user-guide/web-ui.md",
  scenariosDir: "docs/ui-scenarios",
  screenshotsDir: "docs/assets/ui",
  viewport: { width: 1440, height: 980 },
  // Extra Chromium flags for the CDP fallback browser, e.g.
  // CHROME_ARGS=--no-sandbox in rootless containers.
  chromeArgs: (process.env.CHROME_ARGS || "").split(/\s+/).filter(Boolean),

  // The guide runs against the embedded Web UI of a standalone build so the
  // screenshots show exactly what `go build` ships. Requires bun and go.
  async start({ exec, waitForHttp }) {
    await rm(baseDir, { recursive: true, force: true });
    await mkdir(baseDir, { recursive: true });
    await exec("make", ["web-embed"]);
    await exec("go", ["build", "-o", binary, "./cmd/windforce-lite"]);
    server = spawn(
      binary,
      [
        "standalone",
        "--addr", `127.0.0.1:${port}`,
        "--store", path.join(baseDir, "store"),
        "--catalog", path.join(baseDir, "catalog.json"),
        "--state", path.join(baseDir, "state.json"),
        "--cache", path.join(baseDir, "cache"),
      ],
      { cwd: baseDir, stdio: "ignore" },
    );
    server.unref();
    process.on("exit", stopServer);
    await waitForHttp(`http://127.0.0.1:${port}/readyz`, { timeoutMs: 30000 });
    if (!server || server.exitCode !== null) {
      throw new Error(`windforce-lite standalone exited early; is port ${port} already in use?`);
    }
  },

  async seed({ api }) {
    await api("/git_sources/sample", {
      method: "POST",
      body: { app_key: "echo" },
    });
    const sources = await api("/git_sources");
    const sample = sources.find((source) => source.name === "echo");
    if (sample) {
      // A settings change so the Audit tab has a record to show.
      await api(`/git_sources/${sample.id}`, {
        method: "PATCH",
        headers: { "x-windforce-actor": "ui-guide@example.test" },
        body: { name: "echo-service" },
      });
      await api(`/git_sources/${sample.id}/deploy`, {
        method: "POST",
        headers: { "x-windforce-actor": "ui-guide@example.test" },
        body: { confirm: true, message: "UI guide release" },
      });
    }
    await waitForSeedRun(api);
  },
};

async function waitForSeedRun(api) {
  let lastError = "";
  for (let attempt = 0; attempt < 6; attempt += 1) {
    try {
      const result = await api("/jobs/run/echo/echo/wait?timeout_ms=30000", {
        method: "POST",
        body: { message: "hello from the UI guide" },
      });
      if (result.status === "success") return;
      lastError = result.result?.message || result.status || "unknown failure";
    } catch (error) {
      lastError = error.message;
    }
    await sleep(500 * (attempt + 1));
  }
  throw new Error(`seed run did not succeed: ${lastError}`);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
