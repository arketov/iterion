// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { expectNoViolationsStrict } from "@/__tests__/a11y/axeHelpers";

import { IssueCard } from "./IssueCard";
import type { NativeIssue } from "@/api/native";

// PushToForgeButton pulls in AuthContext + server-info; the badge under test
// is independent of it, so stub it to a no-op.
vi.mock("./PushToForge", () => ({ PushToForgeButton: () => null }));

const baseIssue: NativeIssue = {
  id: "native:abc12345",
  title: "needs a decision",
  state: "in_progress",
  created_at: "2026-07-13T10:00:00Z",
  updated_at: "2026-07-13T10:00:00Z",
};

function renderCard(iss: NativeIssue) {
  return render(
    <IssueCard
      iss={iss}
      selected={false}
      activeLabels={new Set()}
      onClick={() => {}}
      onOpen={() => {}}
      onDragStart={() => {}}
      onLabelClick={() => {}}
      onCancelRun={() => {}}
      onOpenRun={() => {}}
      onShowRetryDetails={() => {}}
    />,
  );
}

afterEach(cleanup);

describe("IssueCard approval banner", () => {
  it("renders the approval banner + author when the card carries needs:approval", () => {
    renderCard({
      ...baseIssue,
      labels: ["bug", "needs:approval"],
      external: {
        provider: "github",
        connection_id: "c1",
        repo: "acme/widgets",
        number: 7,
        author: "drive-by",
      },
    });
    expect(screen.getByText(/approval required/i)).toBeTruthy();
    expect(screen.getByText(/@drive-by/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Approve & triage/i })).toBeTruthy();
  });

  it("omits the banner without the needs:approval label", () => {
    renderCard({ ...baseIssue, labels: ["bug"] });
    expect(screen.queryByText(/approval required/i)).toBeNull();
  });
});

describe("IssueCard awaiting-input badge", () => {
  it("renders the ⏸ Awaiting input badge when awaiting_input is set", () => {
    renderCard({ ...baseIssue, awaiting_input: true });
    expect(screen.getByText(/Awaiting input/i)).toBeTruthy();
  });

  it("omits the badge when awaiting_input is falsy", () => {
    renderCard({ ...baseIssue, awaiting_input: false });
    expect(screen.queryByText(/Awaiting input/i)).toBeNull();
    renderCard(baseIssue); // undefined
    expect(screen.queryByText(/Awaiting input/i)).toBeNull();
  });

  it("hides the badge while a live run is in flight (running takes over)", () => {
    render(
      <IssueCard
        iss={{ ...baseIssue, awaiting_input: true }}
        selected={false}
        running={{ run_id: "run-1", issue_id: baseIssue.id } as never}
        activeLabels={new Set()}
        onClick={() => {}}
        onOpen={() => {}}
        onDragStart={() => {}}
        onLabelClick={() => {}}
        onCancelRun={() => {}}
        onOpenRun={() => {}}
        onShowRetryDetails={() => {}}
      />,
    );
    expect(screen.queryByText(/Awaiting input/i)).toBeNull();
  });
});

// The card's run pointer was the only thing the grid said about the run it
// launched (#1501): a card whose run died read exactly like a card that was
// dispatched and worked. The chip now carries the terminal status, and the
// title the cause — the grid has room for a word, the modal for the rest.
describe("IssueCard last-run outcome", () => {
  it("names the terminal status on the chip and the cause in its title", () => {
    renderCard({
      ...baseIssue,
      state: "blocked",
      last_run_id: "run-dead",
      last_run_status: "failed",
      last_run_error_code: "SANDBOX_DRIVER_UNAVAILABLE",
      last_run_error: "sandbox start: no runtime",
    });
    const chip = screen.getByRole("button", { name: /last run/i });
    expect(chip.textContent).toContain("failed");
    expect(chip.className).toContain("text-warning-fg");
    expect(chip.getAttribute("title")).toContain("SANDBOX_DRIVER_UNAVAILABLE");
    expect(chip.getAttribute("title")).toContain("sandbox start: no runtime");
  });

  it("keeps a successful run neutral", () => {
    renderCard({ ...baseIssue, state: "done", last_run_id: "run-ok", last_run_status: "finished" });
    const chip = screen.getByRole("button", { name: /last run/i });
    expect(chip.textContent).toContain("finished");
    expect(chip.className).toContain("text-info");
    expect(chip.className).not.toContain("text-warning-fg");
  });

  it("says nothing about an outcome the spine has not stamped yet", () => {
    renderCard({ ...baseIssue, state: "ready", last_run_id: "run-fresh" });
    const chip = screen.getByRole("button", { name: /last run/i });
    expect(chip.textContent).toBe("↪ last run");
  });

  // in_progress is the live signal on the cloud board: the chip opens the
  // RUNNING run, so a stale terminal status must not be painted over it.
  it("stays the live-run chip while the card is in progress", () => {
    renderCard({ ...baseIssue, state: "in_progress", last_run_id: "run-live", last_run_status: "failed" });
    const chip = screen.getByRole("button", { name: /live run/i });
    expect(chip.textContent).toBe("▶ live run");
    expect(chip.className).toContain("text-info");
  });
});

// The card used to be a role="button" named after its title while holding
// the label chips, the run links and the banner buttons (#2766): invalid
// ARIA, and the two activation paths had drifted apart — the JSX's own
// `onClick` (selection) shadowed the one `clickableRowProps` spread, while
// its `onKeyDown` survived, so Enter opened the modal where a click
// selected the card. The card is no control now; the title is.
describe("IssueCard activation paths", () => {
  it("leaves the card roleless and unnamed — the title is the control", () => {
    const { container } = renderCard(baseIssue);
    const card = container.querySelector("[data-issue-card]") as HTMLElement;
    expect(card.getAttribute("role")).toBeNull();
    expect(card.hasAttribute("tabindex")).toBe(false);
    // An aria-label on the card overrode its own content as the accessible
    // name, and a roleless element cannot carry one at all (RGAA 7.1).
    expect(card.getAttribute("aria-label")).toBeNull();
    const title = screen.getByRole("button", { name: "needs a decision" });
    expect(title.tagName).toBe("BUTTON");
    expect(title.textContent).toBe("needs a decision");
  });

  it("opens from the title by pointer, and selects from the card — never the reverse", () => {
    const onClick = vi.fn();
    const onOpen = vi.fn();
    const { container } = render(
      <IssueCard
        iss={baseIssue}
        selected={false}
        activeLabels={new Set()}
        onClick={onClick}
        onOpen={onOpen}
        onDragStart={() => {}}
        onLabelClick={() => {}}
        onCancelRun={() => {}}
        onOpenRun={() => {}}
        onShowRetryDetails={() => {}}
      />,
    );
    const card = container.querySelector("[data-issue-card]") as HTMLElement;
    // No key on the card does anything a click on it would not do: the
    // card's Enter used to open the modal while its click selected.
    fireEvent.keyDown(card, { key: "Enter" });
    fireEvent.keyDown(card, { key: " " });
    expect(onOpen).not.toHaveBeenCalled();
    expect(onClick).not.toHaveBeenCalled();
    fireEvent.click(card);
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
    // The modal's path is the title control — a native button, so the
    // platform's own Enter/Space reach it (RGAA 7.3).
    fireEvent.click(screen.getByRole("button", { name: "needs a decision" }));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("falls through to multi-select on a modified click of the title", () => {
    const onClick = vi.fn();
    const onOpen = vi.fn();
    render(
      <IssueCard
        iss={baseIssue}
        selected={false}
        activeLabels={new Set()}
        onClick={onClick}
        onOpen={onOpen}
        onDragStart={() => {}}
        onLabelClick={() => {}}
        onCancelRun={() => {}}
        onOpenRun={() => {}}
        onShowRetryDetails={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "needs a decision" }), { shiftKey: true });
    expect(onOpen).not.toHaveBeenCalled();
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("Board · IssueCard — axe smoke, incompletes included", async () => {
    const { container } = render(
      <main>
        <IssueCard
          iss={{ ...baseIssue, labels: ["bug", "a11y"], last_run_id: "run-1", last_run_status: "finished" }}
          selected={false}
          activeLabels={new Set(["bug"])}
          onClick={() => {}}
          onOpen={() => {}}
          onDragStart={() => {}}
          onLabelClick={() => {}}
          onCancelRun={() => {}}
          onOpenRun={() => {}}
          onShowRetryDetails={() => {}}
        />
      </main>,
    );
    await expectNoViolationsStrict(container, "IssueCard");
  });
});

// With require_assignee on, a ready card the dispatcher will not run says why:
// no assignee at all, or an assignee that is not a bot (a person's name, a
// typo). The second used to start the paid default bot without a word.
describe("IssueCard no-bot badge", () => {
  const renderWithSkip = (skip: { kind?: string; assignee?: string; reason: string }) =>
    render(
      <IssueCard
        iss={{ ...baseIssue, state: "ready" }}
        selected={false}
        skip={{ issue_id: baseIssue.id, identifier: "T-1", ...skip }}
        activeLabels={new Set()}
        onClick={() => {}}
        onOpen={() => {}}
        onDragStart={() => {}}
        onLabelClick={() => {}}
        onCancelRun={() => {}}
        onOpenRun={() => {}}
        onShowRetryDetails={() => {}}
      />,
    );

  it("renders “no bot — won't start” for an assignee that is not a bot", () => {
    renderWithSkip({ kind: "no_bot", assignee: "artem", reason: 'assignee "artem" is not a bot of this project' });
    expect(screen.getByText(/no bot — won't start/i)).toBeTruthy();
    expect(screen.queryByText(/no assignee — won't start/i)).toBeNull();
    const badge = screen.getByRole("button", { name: /no bot/i });
    expect(badge.getAttribute("title") ?? "").toContain("artem");
  });

  it("keeps “no assignee — won't start” for a card with no assignee", () => {
    renderWithSkip({ kind: "no_assignee", reason: "no assignee" });
    expect(screen.getByText(/no assignee — won't start/i)).toBeTruthy();
    expect(screen.queryByText(/no bot — won't start/i)).toBeNull();
  });

  it("says the move is being retried, not that the card won't start, for a completion_pending skip", () => {
    renderWithSkip({ kind: "completion_pending", reason: "finished cleanly but the move to \"review\" failed" });
    expect(screen.getByText(/finishing — move retrying/i)).toBeTruthy();
    expect(screen.queryByText(/won't dispatch/i)).toBeNull();
    const badge = screen.getByRole("button", { name: /finishing/i });
    expect(badge.getAttribute("title") ?? "").toContain("will not run again");
  });

  it("renders a neutral queued banner, not a refusal, for a queued skip", () => {
    renderWithSkip({ kind: "queued", reason: "queued — waiting for a slot (ready 4/4)" });
    const banner = screen.getByTestId("queued-banner");
    expect(banner.textContent).toContain("queued — waiting for a slot (ready 4/4)");
    expect(banner.className).not.toMatch(/danger/);
    expect(screen.queryByText(/won't/i)).toBeNull();
    expect(screen.queryByRole("button", { name: /won't|no assignee|no bot/i })).toBeNull();
  });
});

// A refused resume (the bot source changed since the last run started) keeps
// the card in its column and offers the two explicit ways out.
describe("IssueCard source-changed banner", () => {
  const skip = {
    issue_id: baseIssue.id,
    identifier: "T-1",
    kind: "source_changed",
    reason: "bot source changed since run run-1 started",
  };
  const renderBanner = (
    iss: NativeIssue,
    handlers: { onForceResume?: () => void; onRestartFresh?: () => void } = {},
    skipOverride = skip,
  ) =>
    render(
      <IssueCard
        iss={iss}
        selected={false}
        skip={skipOverride}
        activeLabels={new Set()}
        onClick={() => {}}
        onOpen={() => {}}
        onDragStart={() => {}}
        onLabelClick={() => {}}
        onCancelRun={() => {}}
        onOpenRun={() => {}}
        onShowRetryDetails={() => {}}
        {...handlers}
      />,
    );

  it("renders source-changed banner with force and fresh buttons", async () => {
    const onForceResume = vi.fn();
    const onRestartFresh = vi.fn();
    const { container } = renderBanner(
      { ...baseIssue, state: "ready", last_run_id: "run-1" },
      { onForceResume, onRestartFresh },
    );
    fireEvent.click(screen.getByRole("button", { name: /Continue with the new bot version/ }));
    fireEvent.click(screen.getByRole("button", { name: /Start over/ }));
    expect(onForceResume).toHaveBeenCalledTimes(1);
    expect(onRestartFresh).toHaveBeenCalledTimes(1);
    await expectNoViolationsStrict(container, "IssueCard");
  });

  it("disables force when the card has no last run", () => {
    renderBanner({ ...baseIssue, state: "ready" }, { onForceResume: () => {}, onRestartFresh: () => {} });
    const force = screen.getByRole("button", { name: /Continue with the new bot version/ });
    expect((force as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows no way-out buttons for a no_bot skip", () => {
    renderBanner(
      { ...baseIssue, state: "ready", last_run_id: "run-1" },
      { onForceResume: () => {}, onRestartFresh: () => {} },
      { ...skip, kind: "no_bot" },
    );
    expect(screen.queryByRole("button", { name: /Continue with the new bot version/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Start over/ })).toBeNull();
  });
});
