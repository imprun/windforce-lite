export default {
  order: 1,
  id: "control-plane-settings",
  title: "Set control plane context",
  description: "Use the Settings dialog to select the workspace, API token, and actor used by Web UI control-plane requests.",
  screenshot: "docs/assets/ui/control-plane-settings.png",
  guide: [
    "Open Settings from the top bar.",
    "Set the workspace, API token, and actor for control-plane requests.",
    "Apply the context before managing deployments.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Settings");
    await page.waitForSelector("#settingsDialog[open]");
    await capture(this.id);
  },
};
