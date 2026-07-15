export default {
  order: 6,
  id: "action-schemas",
  title: "Review action schemas",
  description:
    "The Docs tab shows each action's release-pinned input and output JSON Schemas.",
  screenshot: "docs/assets/ui/action-schemas.png",
  guide: [
    "Open an app and switch to the Docs tab.",
    "Choose an action from the API reference.",
    "Review its request and result fields or download the source JSON Schema.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#appList .tableRow");
    await page.clickText("Docs");
    await page.click('a[href$="/docs/actions/echo"]');
    await page.waitForText("h3", "Request body");
    await capture(this.id);
  },
};
