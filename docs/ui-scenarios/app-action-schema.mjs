export default {
  order: 4,
  id: "app-action-schema",
  title: "Inspect active deployment contracts",
  description: "Use the active contract detail to inspect the deployed app, actions, deployment history, and materialized source snapshot.",
  screenshot: "docs/assets/ui/deployment-contracts.png",
  guide: [
    "Open the deployment management console.",
    "Select an active app contract.",
    "Review its action list and route tag.",
    "Use Deployment History and Source Snapshot to inspect the deployed contract.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForSelector("#actionList .actionItem");
    await page.waitForText("#deploymentHistory", "external_sync");
    await page.waitForText("#sourceSnapshot", "windforce.json");
    await capture(this.id);
  },
};
