// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { useServerInfoStore } from "@/store/serverInfo";

import Sitemap from "./index";

afterEach(() => {
  cleanup();
  useServerInfoStore.setState({ info: null });
});

function setInfo(info: Record<string, unknown>) {
  useServerInfoStore.setState({ info: info as never, loading: false });
}

function hrefs(): string[] {
  return screen.getAllByRole("link").map((a) => a.getAttribute("href") ?? "");
}

// RGAA 12.1 (#2531): a second navigation system — the plan-du-site page
// generated from the same registry the sidebar renders.
describe("the sitemap page", () => {
  it("renders the registry groups as link lists", () => {
    render(<Sitemap />);
    expect(screen.getByRole("heading", { name: /sitemap/i })).toBeTruthy();
    const links = screen.getAllByRole("link");
    expect(links.length).toBeGreaterThan(5);
    for (const href of ["/", "/whats-next", "/editor", "/runs", "/bots"]) {
      expect(
        links.some((a) => a.getAttribute("href") === href),
        `the sitemap links ${href}`,
      ).toBe(true);
    }
  });

  it("is titled in English", () => {
    render(<Sitemap />);
    expect(
      screen.getByRole("heading", { level: 1, name: "Sitemap" }),
    ).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/plan du site/i);
  });

  it("lists only what a local server without optional features answers", () => {
    setInfo({ mode: "local" });
    render(<Sitemap />);
    const links = hrefs();
    for (const gone of [
      "/board",
      "/dispatcher",
      "/triggers",
      "/marketplace",
      "/secrets",
      "/skills",
      "/integrations",
      "/config-editor",
    ]) {
      expect(links, `${gone} is not available here`).not.toContain(gone);
    }
    expect(links).toContain("/insights");
    // No cloud admin console in local mode.
    expect(links.some((h) => h.startsWith("/admin"))).toBe(false);
    expect(screen.queryByText("Admin console")).toBeNull();
  });

  it("lists a gated page once its server_info flag is on", () => {
    setInfo({ mode: "local", native_tracker_enabled: true, skills_enabled: true });
    render(<Sitemap />);
    const links = hrefs();
    expect(links).toContain("/board");
    expect(links).toContain("/pipelines");
    expect(links).toContain("/skills");
    // The sitemap names the page the way the sidebar does.
    expect(screen.queryByText("Triggers")).toBeNull();
  });

  it("covers the App.tsx route literals (the technical routes allowlisted)", () => {
    const app = readFileSync(
      resolve(process.cwd(), "src/App.tsx"),
      "utf8",
    );
    // The sitemap renders from the hoisted registry (NavLinks.tsx); the
    // coverage check reads the registry AND the page against the routes.
    const registry = readFileSync(
      resolve(process.cwd(), "src/components/shared/NavLinks.tsx"),
      "utf8",
    );
    const sitemap = readFileSync(
      resolve(process.cwd(), "src/views/Sitemap/index.tsx"),
      "utf8",
    );
    // Every top-level content route the app declares must appear in the
    // sitemap source (the allowlist: auth/admin-chrome/technical routes).
    const routes = [...app.matchAll(/<Route path="\/([a-z-]+)"/g)].map(
      (m) => `/${m[1]}`,
    );
    const allowed = new Set([
      "/login", "/signup", "/forgot", "/reset", "/invite", "/welcome",
      "/runs", "/editor", "/board", "/whats-next", "/bots", "/plugins",
      "/marketplace", "/secrets", "/skills", "/triggers", "/settings",
      "/account", "/teams", "/admin", "/config", "/home", "/integrations",
      "/project", "/onboarding",
    ]);
    const missing = routes.filter(
      (r) =>
        !allowed.has(r) &&
        !sitemap.includes(`"${r}"`) &&
        !registry.includes(`href: "${r}"`),
    );
    expect(missing).toEqual([]);
  });

  it("the sidebar footer links the sitemap from every page", () => {
    const sidebar = readFileSync(
      resolve(process.cwd(), "src/components/shared/Sidebar.tsx"),
      "utf8",
    );
    expect(sidebar).toContain('href="/sitemap"');
  });
});
