export default {
  order: 8,
  id: "app-action-schema",
  title: "Inspect active contracts",
  description: "Use the Active Contracts view to inspect the worker-visible app contract, release history, and repository snapshot.",
  screenshot: "docs/assets/ui/deployment-contracts.png",
  guide: [
    "Open the app release console.",
    "Select an active app contract.",
    "Use Contract to review the worker-visible action list and route tag.",
    "Use History to inspect release audit entries.",
    "Use Repository Snapshot to inspect the materialized files used by the release.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click("button[aria-label='Contracts']");
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
