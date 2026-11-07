import { scopePrefix } from "./scope";

// The canvas animations (loop-edge dash flow) run forever, so they must stop
// when nobody can see the canvas. Each document mirrors "nobody is looking"
// onto <html data-view-idle>, and app.css pauses the animations on it — a
// root attribute is live and reaches every edge component without a re-render.
//
// Two sources are OR-ed: the page being hidden (visibilityState) and the
// desktop window being minimised. WebView2 keeps visibilityState "visible"
// for a minimised window, so Go polls it and the shell emits it; the shell
// relays it to every pane iframe by postMessage (a pane has no window.go).

export const WINDOW_MINIMISED = "window-minimised";
export const WINDOW_MINIMISED_REQUEST = "window-minimised-request";

let hidden = false;
let minimised = false;
let installed = false;

function apply(): void {
  document.documentElement.toggleAttribute("data-view-idle", hidden || minimised);
}

/** Record whether the desktop window is minimised and refresh the root attribute. */
export function setWindowMinimised(value: boolean): void {
  minimised = value;
  apply();
}

/** Idempotent: wire visibilitychange and, in a pane, the shell relay. */
export function installViewIdle(): void {
  if (installed || typeof document === "undefined") return;
  installed = true;

  hidden = document.visibilityState === "hidden";
  document.addEventListener("visibilitychange", () => {
    hidden = document.visibilityState === "hidden";
    apply();
  });
  apply();

  const scope = scopePrefix();
  if (!scope || window.parent === window) return;
  window.addEventListener("message", (event: MessageEvent) => {
    const data = event.data as Record<string, unknown> | null;
    if (
      event.origin === window.location.origin &&
      event.source === window.parent &&
      data?.source === "iterion-shell" &&
      data.type === WINDOW_MINIMISED &&
      typeof data.minimised === "boolean"
    ) {
      setWindowMinimised(data.minimised);
    }
  });
  window.parent.postMessage(
    {
      source: "iterion-pane",
      type: WINDOW_MINIMISED_REQUEST,
      projectId: scope.slice(3),
    },
    window.location.origin,
  );
}
