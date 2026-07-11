export default {
  order: 1,
  id: "deployment-overview",
  title: "Review deployment inventory",
  description: "Use the Overview view to check active app deployments, source references, action count, and worker readiness.",
  screenshot: "docs/assets/ui/deployment-overview.png",
  guide: [
    "Open the Overview view.",
    "Check app, source, action, and worker-tag inventory counts.",
    "Use Active Deployments to jump into the selected deployment contract.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForText("#activeDeployments", "echo");
    await capture(this.id);
  },
};
