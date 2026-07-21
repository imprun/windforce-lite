// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test } from "vitest";
import { AppProvider, useApp } from "./app-context";

function SessionProbe() {
  const { settings, logout } = useApp();
  return (
    <div>
      <span data-testid="session">
        {settings.workspace}:{settings.actor}:{settings.token || "signed-out"}
      </span>
      <button type="button" onClick={logout}>
        Log out
      </button>
    </div>
  );
}

describe("AppProvider logout", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("wf.workspace", "gale");
    localStorage.setItem("wf.actor", "operator");
    localStorage.setItem("wf.token", "workspace-secret");
  });

  test("removes browser identity while preserving workspace context", async () => {
    const user = userEvent.setup();
    const queryClient = new QueryClient();
    queryClient.setQueryData(["private"], { value: "cached" });

    render(
      <QueryClientProvider client={queryClient}>
        <AppProvider>
          <SessionProbe />
        </AppProvider>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("session").textContent).toBe("gale:operator:workspace-secret");

    await user.click(screen.getByRole("button", { name: "Log out" }));

    await waitFor(() => {
      expect(screen.getByTestId("session").textContent).toBe("gale::signed-out");
      expect(localStorage.getItem("wf.token")).toBe("");
      expect(localStorage.getItem("wf.actor")).toBe("");
    });
    expect(localStorage.getItem("wf.workspace")).toBe("gale");
    expect(queryClient.getQueryData(["private"])).toBeUndefined();
  });
});
