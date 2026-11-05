// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Router } from "wouter";
import { memoryLocation } from "wouter/memory-location";

import NotFound from "./index";

afterEach(cleanup);

describe("the not-found page", () => {
  it("names the unknown address and links to Home and the Sitemap", () => {
    const { hook } = memoryLocation({ path: "/settings" });
    render(
      <Router hook={hook}>
        <NotFound />
      </Router>,
    );
    expect(
      screen.getByRole("heading", { level: 1, name: "Page not found" }),
    ).toBeTruthy();
    expect(screen.getByText("/settings")).toBeTruthy();
    const hrefs = screen
      .getAllByRole("link")
      .map((a) => a.getAttribute("href"));
    expect(hrefs).toContain("/");
    expect(hrefs).toContain("/sitemap");
  });
});
