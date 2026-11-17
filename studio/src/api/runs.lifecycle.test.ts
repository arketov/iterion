import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "./client";
import {
  ARTIFACT_CONTRACT_INCOMPATIBLE_ERROR_CODE,
  discardRunBranch,
  isForceResumeRequiredError,
  isWorkflowSourceChangedError,
  mergeActionReady,
  resumeRun,
  type RunStatus,
  WORKFLOW_SOURCE_CHANGED_ERROR_CODE,
} from "./runs";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("resume error contract", () => {
  it("preserves error_code on ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: "resume refused",
            error_code: WORKFLOW_SOURCE_CHANGED_ERROR_CODE,
          }),
          {
            status: 400,
            headers: { "Content-Type": "application/json" },
          },
        ),
      ),
    );

    let caught: unknown;
    try {
      await resumeRun("run-1", { answers: { reviewer: "Victor" } });
    } catch (err) {
      caught = err;
    }

    expect(caught).toBeInstanceOf(ApiError);
    expect(caught).toMatchObject({
      status: 400,
      errorCode: WORKFLOW_SOURCE_CHANGED_ERROR_CODE,
      message: "API error 400: resume refused",
    });
    expect(isWorkflowSourceChangedError(caught)).toBe(true);
  });

  it("uses prose only for errors without a structured code", () => {
    expect(
      isWorkflowSourceChangedError(
        new ApiError(400, "API error 400: workflow source has changed"),
      ),
    ).toBe(true);
    expect(
      isWorkflowSourceChangedError(
        new ApiError(
          400,
          "API error 400: workflow source has changed",
          "another_error",
        ),
      ),
    ).toBe(false);
    expect(
      isWorkflowSourceChangedError(
        new ApiError(502, "API error 502: upstream source has changed"),
      ),
    ).toBe(false);
  });

  it("classifies artifact contract refusals as requiring explicit force", () => {
    const err = new ApiError(
      400,
      "API error 400: resume rejected",
      ARTIFACT_CONTRACT_INCOMPATIBLE_ERROR_CODE,
    );
    expect(isForceResumeRequiredError(err)).toBe(true);
    expect(isWorkflowSourceChangedError(err)).toBe(false);
    expect(
      isForceResumeRequiredError(
        new ApiError(400, "API error 400: resume rejected", "another_error"),
      ),
    ).toBe(false);
  });
});

// mergeActionReady gates the "Squash & merge" action (terminal state +
// storage branch). The FilesPanel always defaults to the "combined" (All
// changes) view, so there is no longer a lifecycle-reactive scope default to
// lock down here. Locking this helper keeps the merge-gate contract honest
// without mounting the panel (which needs a query client + Monaco).

describe("mergeActionReady", () => {
  it("is false while the run is still in progress or non-mergeable", () => {
    const notReady: RunStatus[] = [
      "running",
      "paused_waiting_human",
      "paused_operator",
      "failed",
      "failed_resumable",
      "queued",
    ];
    for (const status of notReady) {
      expect(
        mergeActionReady({ status, final_branch: "iterion/run/x" }),
      ).toBe(false);
    }
  });

  it("is false for a terminal run with no storage branch", () => {
    expect(
      mergeActionReady({ status: "finished", final_branch: undefined }),
    ).toBe(false);
    expect(mergeActionReady({ status: "cancelled", final_branch: "" })).toBe(
      false,
    );
  });

  it("is true once terminal with a storage branch (merge button shown)", () => {
    expect(
      mergeActionReady({ status: "finished", final_branch: "iterion/run/x" }),
    ).toBe(true);
    expect(
      mergeActionReady({ status: "cancelled", final_branch: "iterion/run/x" }),
    ).toBe(true);
  });

  it("is false for a missing run", () => {
    expect(mergeActionReady(null)).toBe(false);
    expect(mergeActionReady(undefined)).toBe(false);
  });
});

describe("discardRunBranch", () => {
  it("POSTs the force flag to the run's discard-branch route and resolves on 204", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      discardRunBranch("run/1", { force: true }),
    ).resolves.toBeUndefined();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/runs/run%2F1/discard-branch");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({ force: true });
  });

  it("surfaces the branch_unmerged refusal as an ApiError with its code", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({ error: "3 commits unmerged", error_code: "branch_unmerged" }),
          { status: 409, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    await expect(discardRunBranch("run-1")).rejects.toMatchObject({
      status: 409,
      errorCode: "branch_unmerged",
    });
  });
});
