export default {
  order: 1,
  id: "control-plane-settings",
  title: "Set control plane context",
  description: "Use the Settings page to select the workspace, API token, and actor used by Web UI control-plane requests.",
  screenshot: "docs/assets/ui/control-plane-settings.png",
  guide: [
    "Open Settings from the command bar or sidebar.",
    "Set the workspace and optional API token when the control plane requires one.",
    "Set Actor before deploying a source so audit history has a subject.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click("button[aria-label='Settings']");
    await page.waitForSelector("#settingsPage");
    await page.fill("#actorInput", "ui-guide@example.test");
    await page.clickText("Save Settings");
    await page.waitForText("#settingsPage", "Actor ui-guide@example.test");
    await capture(this.id);
  },
};
