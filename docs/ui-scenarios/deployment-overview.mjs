export default {
  order: 2,
  id: "deployment-overview",
  title: "Review deployment inventory",
  description: "Use the deployment overview to check registered sources, active contracts, credentials, and worker readiness.",
  screenshot: "docs/assets/ui/deployment-overview.png",
  guide: [
    "Open the deployment management console.",
    "Check registered source, active app, credential, and worker counts.",
    "Use Registered FCodes to select the source that will be deployed.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForText("#sourceList", "echo");
    await capture(this.id);
  },
};
