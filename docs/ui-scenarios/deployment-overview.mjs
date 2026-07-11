export default {
  order: 2,
  id: "deployment-overview",
  title: "Manage deployments",
  description: "Use the deployment console to inspect registered sources, active contracts, readiness, and deployment audit evidence.",
  screenshot: "docs/assets/ui/deployment-overview.png",
  guide: [
    "Open the deployment management console.",
    "Use the sidebar to move between deployment, source, release, and audit work areas.",
    "Use the release candidate table to compare registered sources.",
    "Open a source sheet for deployment evidence.",
    "Use the active contracts table to confirm what workers can execute.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForText("#sourceList", "Deployable app sources");
    await page.waitForSelector("#sourceList .tableRow");
    await page.waitForText("#deploymentOverview", "Worker-visible app contracts");
    await capture(this.id);
  },
};
