import { describe, expect, test } from "bun:test";
import { workspaceDetailTabs } from "./WorkspaceDetailPage";

describe("workspaceDetailTabs", () => {
  test("separates identity, access, audit, and lifecycle responsibilities", () => {
    expect(workspaceDetailTabs.map((item) => item.label)).toEqual([
      "Overview",
      "Access",
      "Audit",
      "Lifecycle",
    ]);
  });
});
