// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const standaloneParent = window.parent;
const idle = () => document.documentElement.hasAttribute("data-view-idle");

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => state,
  });
  document.dispatchEvent(new Event("visibilitychange"));
}

// The module keeps its flags at module level, so every test gets a fresh copy.
async function freshModule() {
  vi.resetModules();
  return import("./viewIdle");
}

beforeEach(() => {
  document.documentElement.removeAttribute("data-view-idle");
});

afterEach(() => {
  setVisibility("visible");
  delete (globalThis as { __ITERION_SCOPE__?: string }).__ITERION_SCOPE__;
  Object.defineProperty(window, "parent", {
    configurable: true,
    value: standaloneParent,
  });
});

describe("view idle attribute", () => {
  it("follows the minimised-window signal", async () => {
    const { setWindowMinimised } = await freshModule();
    setWindowMinimised(true);
    expect(idle()).toBe(true);
    setWindowMinimised(false);
    expect(idle()).toBe(false);
  });

  it("follows the page visibility", async () => {
    const { installViewIdle } = await freshModule();
    installViewIdle();
    setVisibility("hidden");
    expect(idle()).toBe(true);
    setVisibility("visible");
    expect(idle()).toBe(false);
  });

  it("stays set while either source holds", async () => {
    const { installViewIdle, setWindowMinimised } = await freshModule();
    installViewIdle();
    setWindowMinimised(true);
    setVisibility("hidden");
    setVisibility("visible");
    expect(idle()).toBe(true);
    setVisibility("hidden");
    setWindowMinimised(false);
    expect(idle()).toBe(true);
    setVisibility("visible");
    expect(idle()).toBe(false);
  });

  it("is idle from the start when the page loads hidden", async () => {
    setVisibility("hidden");
    const { installViewIdle } = await freshModule();
    installViewIdle();
    expect(idle()).toBe(true);
  });

  it("in a pane accepts only the shell's own message and asks for the state", async () => {
    const parent = { postMessage: vi.fn() } as unknown as Window;
    Object.defineProperty(window, "parent", { configurable: true, value: parent });
    (globalThis as { __ITERION_SCOPE__?: string }).__ITERION_SCOPE__ = "/x/p1";
    const { installViewIdle, WINDOW_MINIMISED, WINDOW_MINIMISED_REQUEST } = await freshModule();
    installViewIdle();

    expect(parent.postMessage).toHaveBeenCalledWith(
      { source: "iterion-pane", type: WINDOW_MINIMISED_REQUEST, projectId: "p1" },
      window.location.origin,
    );

    const send = (init: { origin?: string; source?: Window; minimised?: unknown }) =>
      window.dispatchEvent(
        new MessageEvent("message", {
          origin: init.origin ?? window.location.origin,
          source: init.source ?? parent,
          data: { source: "iterion-shell", type: WINDOW_MINIMISED, minimised: init.minimised ?? true },
        }),
      );

    send({ origin: "http://foreign.test" });
    send({ source: window });
    send({ minimised: "yes" });
    expect(idle()).toBe(false);

    send({});
    expect(idle()).toBe(true);
    send({ minimised: false });
    expect(idle()).toBe(false);
  });
});
