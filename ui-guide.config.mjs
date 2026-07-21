import { spawn } from "node:child_process";
import { mkdir, rm } from "node:fs/promises";
import { createServer as createHttpServer } from "node:http";
import path from "node:path";

const port = Number(process.env.WINDFORCE_LITE_UI_GUIDE_PORT || 18099);
const external = process.env.WINDFORCE_LITE_UI_GUIDE_EXTERNAL === "true";
const baseDir = path.resolve(".tmp/ui-guide");
const binary = path.resolve(
  baseDir,
  process.platform === "win32" ? "windforce-core.exe" : "windforce-core",
);

let server = null;
let receiver = null;
let receiverUrl = "";

function stopServer() {
  if (server && server.exitCode === null) server.kill();
  server = null;
  if (receiver) receiver.close();
  receiver = null;
  receiverUrl = "";
}

export default {
  name: "windforce-core",
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
    if (external) {
      await waitForHttp(this.baseUrl);
      return;
    }
    await rm(baseDir, { recursive: true, force: true });
    await mkdir(baseDir, { recursive: true });
    await exec("make", ["web-embed"]);
    await exec("go", ["build", "-o", binary, "./cmd/windforce-core"]);
    receiver = createHttpServer((request, response) => {
      const signature = request.headers["x-windforce-signature"];
      const eventID = request.headers["x-windforce-event"];
      let body = "";
      request.on("data", (chunk) => { body += chunk; });
      request.on("end", () => {
        if (request.method !== "POST" || !signature || !eventID || !body) {
          response.writeHead(400).end();
          return;
        }
        response.writeHead(204).end();
      });
    });
    await new Promise((resolve, reject) => {
      receiver.once("error", reject);
      receiver.listen(0, "127.0.0.1", resolve);
    });
    receiver.unref();
    const address = receiver.address();
    receiverUrl = `http://127.0.0.1:${address.port}/windforce/releases`;
    server = spawn(
      binary,
      [
        "standalone",
        "--dev",
        "--addr", `127.0.0.1:${port}`,
        "--store", path.join(baseDir, "store"),
        "--catalog", path.join(baseDir, "catalog.json"),
        "--state", path.join(baseDir, "state.json"),
        "--cache", path.join(baseDir, "cache"),
        "--webhook-allow-insecure-loopback",
      ],
      { cwd: baseDir, stdio: "ignore" },
    );
    server.unref();
    process.on("exit", stopServer);
    await waitForHttp(`http://127.0.0.1:${port}/readyz`, { timeoutMs: 30000 });
    if (!server || server.exitCode !== null) {
      throw new Error(`windforce-core standalone exited early; is port ${port} already in use?`);
    }
  },

  async seed({ api, exec }) {
    if (external) return;
    const workspaceResponse = await fetch(new URL("/api/workspaces", this.baseUrl), {
      method: "POST",
      headers: { "content-type": "application/json", "x-windforce-actor": "ui-guide@example.test" },
      body: JSON.stringify({ id: "operations", name: "Operations" }),
    });
    if (!workspaceResponse.ok) {
      throw new Error(`workspace seed failed: HTTP ${workspaceResponse.status} ${await workspaceResponse.text()}`);
    }
    await api("/git_sources/sample", {
      method: "POST",
      body: { app_key: "echo" },
    });
    const sources = await api("/git_sources");
    const sample = sources.find((source) => source.name === "echo");
    let webhook = null;
    if (sample) {
      // A settings change so the Audit tab has a record to show.
      await api(`/git_sources/${sample.id}`, {
        method: "PATCH",
        headers: { "x-windforce-actor": "ui-guide@example.test" },
        body: { name: "echo-service" },
      });
      webhook = await api("/webhooks", {
        method: "POST",
        headers: { "x-windforce-actor": "ui-guide@example.test" },
        body: {
          name: "Release notifications",
          endpoint: receiverUrl,
          event_types: ["windforce.release.published"],
          app_keys: ["echo"],
          enabled: true,
        },
      });
      await api(`/git_sources/${sample.id}/deploy`, {
        method: "POST",
        headers: { "x-windforce-actor": "ui-guide@example.test" },
        body: { confirm: true, message: "UI guide release" },
      });
      await waitForWebhookDelivery(api, webhook.subscription.id);
      await advanceSampleRepository(exec);
    }
    const clientToken = await api("/clients", {
      method: "POST",
      headers: { "x-windforce-actor": "ui-guide@example.test" },
      body: { name: "Example Retailer" },
    });
    await api("/apps/echo/input-configs", {
      method: "PUT",
      headers: { "x-windforce-actor": "ui-guide@example.test" },
      body: {
        action_key: "echo",
        client_id: clientToken.client.id,
        config: { message: "configured for Example Retailer", response_mode: "compact" },
        locked_keys: ["message"],
      },
    });
    await waitForClientConfigRun(clientToken.client.id, clientToken.api_token);
    // The standalone local store replaces its JSON file after the worker
    // publishes the terminal result. Let that final Windows file operation
    // settle before browser scenarios begin issuing concurrent reads.
    await sleep(500);
  },

  async stop() {
    stopServer();
  },
};

async function advanceSampleRepository(exec) {
  const sampleBase = path.join(baseDir, ".data", "sample-repos", "default", "echo");
  const worktree = path.join(sampleBase, "work");
  const remote = path.join(sampleBase, "remote.git");
  await exec("git", ["-C", worktree, "commit", "--allow-empty", "-m", "Prepare next sample release"]);
  await exec("git", ["-C", worktree, "push", remote, "HEAD:refs/heads/main"]);
}

async function waitForWebhookDelivery(api, subscriptionID) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const page = await api(`/webhooks/${encodeURIComponent(subscriptionID)}/deliveries?limit=5`);
    const release = page.items.find((item) => item.event.type === "windforce.release.published");
    if (release?.delivery.state === "succeeded") return;
    if (release?.delivery.state === "failed") {
      throw new Error(`UI guide webhook delivery failed: ${release.delivery.error_summary || "unknown error"}`);
    }
    await sleep(100);
  }
  throw new Error("UI guide webhook delivery did not succeed");
}

async function waitForClientConfigRun(clientID, apiToken) {
  const runsURL = `http://127.0.0.1:${port}/execution/v1/workspaces/default/runs`;
  const publicURL = `http://127.0.0.1:${port}/api/v1/w/default/run/echo/echo`;
  const headers = { "authorization": `Bearer ${apiToken}`, "content-type": "application/json" };
  const rejected = await fetch(publicURL, {
    method: "POST",
    headers,
    body: JSON.stringify({ message: "caller value" }),
  });
  if (rejected.status !== 400) {
    throw new Error(`locked input was not rejected: HTTP ${rejected.status}`);
  }

  const admitted = await fetch(publicURL, {
    method: "POST",
    headers,
    body: JSON.stringify({}),
  });
  if (!admitted.ok) {
    throw new Error(`client-config run admission failed: HTTP ${admitted.status} ${await admitted.text()}`);
  }
  const run = await admitted.json();

  for (let attempt = 0; attempt < 60; attempt += 1) {
    const response = await fetch(`${runsURL}/${encodeURIComponent(run.job_id)}/result`);
    const result = await response.json();
    if (response.status === 202) {
      await sleep(250);
      continue;
    }
    if (!response.ok) {
      throw new Error(`client-config run failed: HTTP ${response.status} ${JSON.stringify(result)}`);
    }
    if (result.output?.input?.message !== "configured for Example Retailer") {
      throw new Error(`worker did not apply client input settings: ${JSON.stringify(result)}`);
    }
    if (result.client_id && result.client_id !== clientID) {
      throw new Error(`public run used the wrong client: ${JSON.stringify(result)}`);
    }
    return;
  }
  throw new Error("client-config run did not finish");
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
