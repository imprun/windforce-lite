export default {
  name: "windforce-lite",
  baseUrl: process.env.WINDFORCE_LITE_UI_URL || "http://127.0.0.1:18090/ui/",
  apiBaseUrl: process.env.WINDFORCE_LITE_API_URL || "http://127.0.0.1:18091/api/w/default",
  guidePath: "docs/user-guide/web-ui.md",
  scenariosDir: "docs/ui-scenarios",
  screenshotsDir: "docs/assets/ui",
  viewport: { width: 1440, height: 980 },

  async start({ exec, waitForHttp }) {
    await exec("docker", ["compose", "up", "-d", "postgres", "control-plane", "web", "worker"]);
    await waitForHttp("http://127.0.0.1:18091/readyz", { timeoutMs: 60000 });
    await waitForHttp("http://127.0.0.1:18090/ui/", { timeoutMs: 60000 });
  },

  async seed({ api }) {
    await resetGitSources(api);
    await api("/git_sources/sample", {
      method: "POST",
      body: { app_key: "echo" },
    });
    await waitForSeedRun(api);
  },
};

async function resetGitSources(api) {
  const sources = await api("/git_sources");
  await Promise.all(
    sources.map((source) =>
      api(`/git_sources/${encodeURIComponent(source.id)}`, {
        method: "DELETE",
      }),
    ),
  );
}

async function waitForSeedRun(api) {
  let lastError = "";
  for (let attempt = 0; attempt < 5; attempt += 1) {
    try {
      const result = await api("/jobs/run/echo/echo/wait?timeout_ms=30000", {
        method: "POST",
        body: { message: "ui-guide-seed" },
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
