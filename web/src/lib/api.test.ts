import { describe, expect, test } from "bun:test";
import { WindforceApi, setActorHeaders } from "./api";

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

describe("WindforceApi documentation URLs", () => {
  test("uses the configured workspace for OpenAPI and action invocation links", () => {
    const api = new WindforceApi({ workspace: "team-a", token: "", actor: "" });

    expect(api.appOpenAPIURL("echo")).toBe("/api/w/team-a/apps/echo/openapi.json");
    expect(api.actionRunURL("echo", "notify.send")).toBe("/api/w/team-a/jobs/run/echo/notify.send");
  });
});
