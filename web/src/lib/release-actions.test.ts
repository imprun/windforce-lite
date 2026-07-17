import { describe, expect, test } from "bun:test";
import { releaseActionState } from "./release-actions";

describe("releaseActionState", () => {
  test("requires synchronization before the first release", () => {
    expect(releaseActionState(undefined, undefined, false)).toEqual({
      syncLabel: "Sync source",
      syncDisabled: false,
      publishLabel: "Sync required",
      publishDisabled: true,
    });
  });

  test("enables publication when synchronized source differs from active release", () => {
    expect(releaseActionState("active", "latest", true)).toEqual({
      syncLabel: "Source current",
      syncDisabled: true,
      publishLabel: "Publish Release",
      publishDisabled: false,
    });
  });

  test("prevents an accidental duplicate release", () => {
    expect(releaseActionState("same", "same", true)).toEqual({
      syncLabel: "Source current",
      syncDisabled: true,
      publishLabel: "Up to date",
      publishDisabled: true,
    });
  });
});
