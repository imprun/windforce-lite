export default {
  order: 6,
  id: "action-schemas",
  title: "Review action schemas",
  description:
    "The Actions tab shows each action's materialized input and output JSON Schemas — the contract workers and callers rely on.",
  screenshot: "docs/assets/ui/action-schemas.png",
  guide: [
    "Open an app and switch to the Actions tab.",
    "Review the input and output JSON Schemas materialized from the release.",
    "Invoke actions through the control-plane API or the CLI; the UI documents the contract only.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#appList .tableRow");
    await page.clickText("Actions");
    await page.waitForText("h2", "Action · echo");
    await capture(this.id);
  },
};
