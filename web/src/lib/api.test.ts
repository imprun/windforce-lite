import { describe, expect, test } from "bun:test";
import { setActorHeaders, webhookAppKeys, WindforceApi, type WebhookSubscription } from "./api";

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

describe("WindforceApi release flow", () => {
  test("syncs without publishing and publishes the exact candidate commit", async () => {
    const requests: Array<{ url: string; method: string; body: string }> = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = (async (input, init) => {
      requests.push({
        url: String(input),
        method: init?.method || "GET",
        body: String(init?.body || ""),
      });
      return new Response(JSON.stringify({ app: "echo", commit: "commit-a", actions: [] }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }) as typeof fetch;
    try {
      const api = new WindforceApi({ workspace: "default", token: "", actor: "operator" });
      await api.syncGitSource(7);
      await api.deployGitSource(7, "commit-a", "Release note");

      expect(requests).toEqual([
        {
          url: "/api/w/default/git_sources/7/sync",
          method: "POST",
          body: "",
        },
        {
          url: "/api/w/default/git_sources/7/deploy",
          method: "POST",
          body: JSON.stringify({ confirm: true, commit: "commit-a", message: "Release note" }),
        },
      ]);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});

describe("WindforceApi webhooks", () => {
  test("treats a null app scope from the API as all apps", () => {
    expect(webhookAppKeys({ app_keys: null } as WebhookSubscription)).toEqual([]);
  });

  test("lists deleted subscriptions only when requested", async () => {
    const calls: string[] = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = (async (input) => {
      calls.push(String(input));
      return new Response("[]", { status: 200 });
    }) as typeof fetch;
    try {
      const api = new WindforceApi({ workspace: "demo", token: "", actor: "operator" });
      await api.webhookSubscriptions();
      await api.webhookSubscriptions(true);
      expect(calls).toEqual([
        "/api/w/demo/webhooks",
        "/api/w/demo/webhooks?include_deleted=true",
      ]);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  test("creates a scoped release subscription with the actor header", async () => {
    const requests: Array<{ url: string; method: string; headers: Headers; body: string }> = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = (async (input, init) => {
      requests.push({
        url: String(input),
        method: init?.method || "GET",
        headers: new Headers(init?.headers),
        body: String(init?.body || ""),
      });
      return new Response(JSON.stringify({ subscription: { id: "wh_1" }, signing_secret: "shown-once" }), {
        status: 201,
        headers: { "content-type": "application/json" },
      });
    }) as typeof fetch;
    try {
      const api = new WindforceApi({ workspace: "default", token: "token", actor: "운영자" });
      await api.createWebhookSubscription({
        name: "Release notifications",
        endpoint: "https://hooks.example.test/releases",
        event_types: ["windforce.release.published"],
        app_keys: ["echo"],
        enabled: true,
      });
      expect(requests).toHaveLength(1);
      expect(requests[0].url).toBe("/api/w/default/webhooks");
      expect(requests[0].method).toBe("POST");
      expect(decodeUTF8Base64(requests[0].headers.get("x-windforce-actor-utf8") || "")).toBe("운영자");
      expect(JSON.parse(requests[0].body)).toEqual({
        name: "Release notifications",
        endpoint: "https://hooks.example.test/releases",
        event_types: ["windforce.release.published"],
        app_keys: ["echo"],
        enabled: true,
      });
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  test("encodes delivery filters and retry identifiers", async () => {
    const calls: Array<{ url: string; method: string }> = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = (async (input, init) => {
      calls.push({ url: String(input), method: init?.method || "GET" });
      const body = String(input).endsWith("/retry") ? { delivery: {}, event: {} } : { items: [] };
      return new Response(JSON.stringify(body), { status: 200 });
    }) as typeof fetch;
    try {
      const api = new WindforceApi({ workspace: "default", token: "", actor: "operator" });
      await api.webhookDeliveries("wh/1", { state: "failed", limit: 25, cursor: "time/id" });
      await api.retryWebhookDelivery("delivery/1");
      expect(calls).toEqual([
        {
          url: "/api/w/default/webhooks/wh%2F1/deliveries?state=failed&limit=25&cursor=time%2Fid",
          method: "GET",
        },
        { url: "/api/w/default/webhook-deliveries/delivery%2F1/retry", method: "POST" },
      ]);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
