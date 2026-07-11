export default {
  order: 1,
  id: "control-plane-settings",
  title: "Set control plane context",
  description: "Use Settings to select the workspace, API token, and actor used by Web UI control-plane requests.",
  screenshot: "docs/assets/ui/control-plane-settings.png",
  guide: [
    "Open Settings from the command bar or sidebar.",
    "Set the workspace and optional API token when the control plane requires one.",
    "Set Actor before deployment so audit history has an operator subject.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click("#openSettings");
    await page.waitForSelector("#settingsDialog");
    await page.fill("#actorInput", "ui-guide@example.test");
    await capture(this.id);
  },
};
