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

describe("provisioning layout", () => {
  test("keeps provisioning as task cards inside settings", () => {
    expect(styles).toMatch(/\.provisioningTaskGrid\s*\{[^}]*grid-template-columns:\s*repeat\(2,\s*minmax\(360px,\s*1fr\)\);/s);
    expect(styles).toMatch(/\.provisioningTask\s*\{[^}]*min-height:\s*236px;/s);
    expect(styles).toMatch(/\.provisioningEditor\s*\{[^}]*min-height:\s*320px;/s);
    expect(styles).toMatch(/\.provisioningCode\s*\{[^}]*max-height:\s*460px;/s);
  });
});
