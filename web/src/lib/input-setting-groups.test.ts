import { describe, expect, test } from "bun:test";
import { type InputConfig } from "./api";
import { groupInputSettings, inputSettingGroupMatches, paginate } from "./input-setting-groups";

function config(patch: Partial<InputConfig>): InputConfig {
  return {
    workspace_id: "default",
    app_key: "orders",
    action_key: "",
    config: {},
    locked_keys: [],
    updated_by: "operator-a",
    updated_at: "2026-07-16T01:00:00Z",
    ...patch,
  };
}

describe("groupInputSettings", () => {
  test("summarizes scopes, values, locks, and the latest change", () => {
    const groups = groupInputSettings(
      [
        config({ action_key: "list", client_id: "client-a", config: { region: "kr" } }),
        config({
          action_key: "detail",
          client_id: "client-a",
          config: { region: "us", retries: 2 },
          locked_keys: ["retries"],
          updated_by: "operator-b",
          updated_at: "2026-07-16T02:00:00Z",
        }),
      ],
      (item) => item.client_id || "default",
    );

    expect(groups).toHaveLength(1);
    expect(groups[0]).toMatchObject({
      key: "client-a",
      actionKeys: ["list", "detail"],
      keyNames: ["region", "retries"],
      valueCount: 3,
      lockedCount: 1,
      updatedBy: "operator-b",
    });
  });

  test("matches labels, action keys, and configured key names", () => {
    const [group] = groupInputSettings([config({ action_key: "login", config: { proxy: null } })], () => "default");
    expect(inputSettingGroupMatches(group, "Blue Client", ["Blue Client"])).toBeTrue();
    expect(inputSettingGroupMatches(group, "login")).toBeTrue();
    expect(inputSettingGroupMatches(group, "proxy")).toBeTrue();
    expect(inputSettingGroupMatches(group, "missing")).toBeFalse();
  });
});

describe("paginate", () => {
  test("clamps page bounds and slices the requested page", () => {
    expect(paginate([1, 2, 3, 4, 5], 2, 2)).toEqual({ items: [3, 4], page: 2, totalPages: 3, start: 2 });
    expect(paginate([1, 2, 3], 99, 2).page).toBe(2);
    expect(paginate([], 1, 25)).toEqual({ items: [], page: 1, totalPages: 1, start: 0 });
  });
});
