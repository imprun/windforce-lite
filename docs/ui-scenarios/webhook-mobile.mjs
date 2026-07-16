export default {
  order: 14,
  id: "webhook-mobile",
  title: "Review webhooks on a narrow screen",
  description:
    "The webhook list remains usable on narrow screens through a stable navigation rail and horizontally scrollable operational table.",
  screenshot: "docs/assets/ui/webhook-mobile.png",
  viewport: { width: 390, height: 844 },
  guide: [
    "Open Settings and choose Webhooks on a narrow screen.",
    "Scroll the table horizontally when all operational columns are needed.",
    "Open the webhook name to continue into its full detail page.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Settings");
    await page.clickText("Webhooks");
    await page.waitForSelector("#webhookList tbody tr");
    await capture(this.id);
  },
};
