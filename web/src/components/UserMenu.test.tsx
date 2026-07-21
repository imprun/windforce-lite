// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { AppProvider } from "../lib/app-context";
import { RouterProvider } from "../lib/router";
import { UserMenu } from "./Layout";

describe("UserMenu", () => {
  afterEach(cleanup);

  beforeEach(() => {
    localStorage.clear();
  });

  function renderMenu() {
    const queryClient = new QueryClient();
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider>
          <AppProvider>
            <UserMenu />
          </AppProvider>
        </RouterProvider>
      </QueryClientProvider>,
    );
  }

  test("offers logout when only an audit actor is stored", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(screen.getByRole("button", { name: "User menu for local-dev" }));

    expect(screen.getByText("API token not configured")).toBeTruthy();
    expect(
      screen.getByRole("menuitem", { name: "Log out" }).getAttribute("data-disabled"),
    ).toBeNull();
  });

  test("offers logout when a browser token is stored", async () => {
    const user = userEvent.setup();
    localStorage.setItem("wf.token", "workspace-secret");
    renderMenu();

    await user.click(screen.getByRole("button", { name: "User menu for local-dev" }));

    expect(screen.getByText("API token configured")).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Log out" })).toBeTruthy();
  });
});
