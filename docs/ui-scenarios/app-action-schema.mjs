export default {
  order: 3,
  id: "app-action-schema",
  title: "Inspect active deployment contracts",
  description: "Use the Contracts view to inspect the active app deployment, action contracts, and materialized source snapshot.",
  screenshot: "docs/assets/ui/deployment-contracts.png",
  guide: [
    "Open the Contracts view.",
    "Select a deployed app.",
    "Select an action to load input and output schemas.",
    "Use Action Schema, Deployments, and Source Snapshot tabs to inspect the deployed contract.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Contracts");
    await page.waitForSelector("#actionList [data-action]");
    await page.click("#actionList [data-action]");
    await page.waitForText("#schemaTab", "input_schema");
    await capture(this.id);
  },
};
