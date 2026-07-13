import { describe, expect, test } from "bun:test";

const styles = await Bun.file(new URL("./styles.css", import.meta.url)).text();

describe("table column alignment", () => {
  test("does not override every non-first table header", () => {
    expect(styles).not.toContain(".table th:not(:first-child)");
  });

  test("uses the shared numeric cell alignment contract", () => {
    expect(styles).toMatch(/\.numCell\s*\{[^}]*text-align:\s*right;/s);
    expect(styles).toMatch(/\.table th\.numCell\s*\{[^}]*text-align:\s*right;/s);
  });
});
