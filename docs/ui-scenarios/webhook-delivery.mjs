export default {
  order: 13,
  id: "webhook-delivery",
  title: "Inspect a webhook delivery",
  description:
    "Delivery history exposes status, response, attempts, immutable event data, and a guarded retry action for failed deliveries.",
  screenshot: "docs/assets/ui/webhook-delivery.png",
  guide: [
    "Open a webhook and switch to Deliveries.",
    "Select an event to inspect the attempt in a sheet without leaving the history table.",
    "Review the immutable event data and use Retry only when a delivery has failed.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Settings");
    await page.clickText("Webhooks");
    await page.waitForSelector("#webhookList tbody tr");
    await page.clickText("Release notifications");
    await page.clickText("Deliveries");
    await page.waitForSelector("#webhookDeliveries tbody tr");
    await page.clickText("Release published");
    await page.waitForSelector("#webhookDeliverySheet");
    await capture(this.id);
  },
};
