export default {
  order: 8,
  id: "app-action-schema",
  title: "Inspect active deployment contracts",
  description: "Use the Releases view to inspect the deployed app contract, history, and source snapshot.",
  screenshot: "docs/assets/ui/deployment-contracts.png",
  guide: [
    "Open the deployment management console.",
    "Select an active app contract.",
    "Use Contract to review the worker-visible action list and route tag.",
    "Use History to inspect deployment audit entries.",
    "Use Source Snapshot to inspect the materialized files used by the release.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click("button[aria-label='Releases']");
    await page.waitForSelector("#appDetail");
    await page.click("#tab-contract");
    await page.waitForSelector("#actionList .actionRow");
    await page.click("#tab-history");
    await page.waitForSelector("#deploymentHistory .historyItem");
    await page.click("#tab-source");
    await page.waitForText("#sourceSnapshot", "windforce.json");
    await capture(this.id);
  },
};
