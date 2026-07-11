export default {
  order: 1,
  id: "control-plane-settings",
  title: "Set control plane context",
  description: "Use the top bar to select the workspace, API token, and actor used by Web UI control-plane requests.",
  screenshot: "docs/assets/ui/control-plane-settings.png",
  guide: [
    "Set the workspace in the top bar.",
    "Set the optional API token when the control plane requires one.",
    "Set Actor before deployment so audit history has an operator subject.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.fill("#actorInput", "ui-guide@example.test");
    await capture(this.id);
  },
};
