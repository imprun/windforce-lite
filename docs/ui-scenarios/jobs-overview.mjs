export default {
  order: 7,
  id: "jobs-overview",
  title: "Monitor job activity",
  description:
    "The Jobs view aggregates run activity for the whole workspace: totals, per-app and per-route-tag breakdowns, and failure rates. Individual runs are an API/CLI concern.",
  screenshot: "docs/assets/ui/jobs-overview.png",
  guide: [
    "Open Jobs from the sidebar.",
    "Read the tiles: queued and running now, plus completed, failed, and canceled runs in the selected window.",
    "Switch the window between 1h, 24h, and 7d.",
    "Use the by-app and by-route-tag tables to find where the failure rate is moving; app names link to the app detail.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Jobs");
    await page.waitForSelector("#jobSummary");
    await page.waitForSelector("#jobsByApp .tableRow");
    await capture(this.id);
  },
};
