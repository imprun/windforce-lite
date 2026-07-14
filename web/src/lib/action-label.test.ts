import { describe, expect, test } from "bun:test";
import { actionDisplayName } from "./action-label";

describe("actionDisplayName", () => {
  test("removes implementation notes and input suffixes", () => {
    expect(actionDisplayName("정산내역조회 입력값 (4MDCPCM M1 — runtime notes)")).toBe("정산내역조회");
    expect(actionDisplayName("Coupang Eats login session issue input")).toBe("Coupang Eats login session issue");
  });

  test("preserves concise names and empty values", () => {
    expect(actionDisplayName("Create order")).toBe("Create order");
    expect(actionDisplayName("   ")).toBeNull();
  });
});
