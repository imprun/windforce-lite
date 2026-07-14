import { describe, expect, test } from "bun:test";
import { setActorHeaders } from "./api";

function decodeUTF8Base64(value: string): string {
  const binary = atob(value);
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

describe("setActorHeaders", () => {
  test("keeps ASCII actors on the legacy header", () => {
    const headers = new Headers();

    setActorHeaders(headers, " local-dev ");

    expect(headers.get("x-windforce-actor")).toBe("local-dev");
    expect(headers.get("x-windforce-actor-utf8")).toBeNull();
  });

  test("encodes Unicode actors into an ASCII-safe header", () => {
    const headers = new Headers();

    setActorHeaders(headers, "홍길동");

    expect(headers.get("x-windforce-actor")).toBeNull();
    const encoded = headers.get("x-windforce-actor-utf8");
    expect(encoded).toBeTruthy();
    expect(decodeUTF8Base64(encoded || "")).toBe("홍길동");
  });
});
