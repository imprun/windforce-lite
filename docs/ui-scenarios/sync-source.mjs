export default {
  order: 3.5,
  id: "sync-source",
  title: "Synchronize source",
  description:
    "Sync source fetches the tracked branch, validates the source contract, and stores the exact revision without changing the active release.",
  screenshot: "docs/assets/ui/sync-source.png",
  guide: [
    "Open an app and find Sync source next to Publish Release.",
    "Click Sync source.",
    "Confirm that Sync source changes to Source current and Publish Release becomes available when the commit changed.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#appList .tableRow");
    await page.click("#syncSourceButton");
    await page.waitForSelector('#syncSourceButton[data-checked="true"]');
    await capture(this.id);
  },
};
