import { describe, expect, test } from "bun:test";
import { inputConfigPayload } from "./InputConfigDialog";

describe("inputConfigPayload", () => {
  test("builds JSON values and locked keys", () => {
    expect(
      inputConfigPayload(
        [
          { key: "region", valueText: '"kr"', locked: false },
          { key: "limits", valueText: '{"daily":10}', locked: true },
        ],
        "orders",
        "client-a",
      ),
    ).toEqual({
      action_key: "orders",
      client_id: "client-a",
      config: { region: "kr", limits: { daily: 10 } },
      locked_keys: ["limits"],
    });
  });

  test("rejects duplicate keys and invalid JSON", () => {
    expect(() =>
      inputConfigPayload(
        [
          { key: "region", valueText: '"kr"', locked: false },
          { key: "region", valueText: '"us"', locked: false },
        ],
        "",
        "",
      ),
    ).toThrow("Duplicate key");
    expect(() => inputConfigPayload([{ key: "region", valueText: "kr", locked: false }], "", "")).toThrow(
      "valid JSON",
    );
  });
});
