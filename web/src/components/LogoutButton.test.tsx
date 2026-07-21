// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test } from "vitest";
import { AppProvider } from "../lib/app-context";
import { RouterProvider } from "../lib/router";
import { LogoutButton } from "./Layout";

describe("LogoutButton", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  function renderButton() {
    const queryClient = new QueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider>
          <AppProvider>
            <LogoutButton />
          </AppProvider>
        </RouterProvider>
      </QueryClientProvider>,
    );
  }

  test("shows the signed-out state when no API token is stored", () => {
    renderButton();

    const button = screen.getByRole("button", { name: "Signed out" });
    expect(button).toBeTruthy();
    expect(button.hasAttribute("disabled")).toBe(true);
    expect(screen.getByText("Signed out")).toBeTruthy();
  });

  test("shows a visible logout action when an API token is stored", () => {
    localStorage.setItem("wf.token", "workspace-secret");
    renderButton();

    expect(screen.getByRole("button", { name: "Log out" })).toBeTruthy();
    expect(screen.getByText("Log out")).toBeTruthy();
  });
});
