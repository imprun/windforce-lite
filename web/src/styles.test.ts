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
  test("keeps editor and export panes at stable dimensions", () => {
    expect(styles).toMatch(/\.provisioningGrid\s*\{[^}]*grid-template-columns:\s*minmax\(360px,\s*0\.9fr\)\s*minmax\(420px,\s*1\.1fr\);/s);
    expect(styles).toMatch(/\.provisioningEditor\s*\{[^}]*min-height:\s*420px;/s);
    expect(styles).toMatch(/\.provisioningCode\s*\{[^}]*min-height:\s*420px;/s);
  });
});
