// Extracted from api/runs.ts to keep that file focused.
// Commits panel — git log between BaseCommit and FinalCommit/HEAD,
// plus the deferred-merge + commit-and-finalize endpoints invoked by
// the CommitsPanel actions.

import { request } from "./client";
import type {
  CommitAndFinalizeRequest,
  CommitAndFinalizeResponse,
  MergeRunRequest,
  MergeRunResponse,
  RunCommitDetail,
  RunCommits,
  RunFileDiff,
} from "./types";

export async function listRunCommits(runId: string): Promise<RunCommits> {
  return request(`/runs/${encodeURIComponent(runId)}/commits`);
}

export async function getRunCommit(
  runId: string,
  sha: string,
): Promise<RunCommitDetail> {
  return request(
    `/runs/${encodeURIComponent(runId)}/commits/${encodeURIComponent(sha)}`,
  );
}

export async function getRunCommitFileDiff(
  runId: string,
  sha: string,
  path: string,
): Promise<RunFileDiff> {
  return request(
    `/runs/${encodeURIComponent(runId)}/commits/${encodeURIComponent(
      sha,
    )}/diff?path=${encodeURIComponent(path)}`,
  );
}

export async function mergeRun(
  runId: string,
  req: MergeRunRequest,
): Promise<MergeRunResponse> {
  return request(`/runs/${encodeURIComponent(runId)}/merge`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// discardRunBranch deletes a finished run's iterion/run/<id> storage branch
// from the local repository (the server answers 204). A branch whose commits
// are not merged is refused with 409 and errorCode "branch_unmerged" until
// the call is repeated with force: true.
export async function discardRunBranch(
  runId: string,
  req: { force?: boolean } = {},
): Promise<void> {
  await request<void>(`/runs/${encodeURIComponent(runId)}/discard-branch`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

// commitAndFinalizeRun stages and commits a run's uncommitted workdir
// changes with the operator-supplied message, then promotes the new
// HEAD onto a persistent branch (FinalCommit + FinalBranch land on
// the run record). Used when a bot finishes a work session without
// committing — the operator salvages the diff from the run page
// instead of having to commit by hand in the workspace.
export async function commitAndFinalizeRun(
  runId: string,
  req: CommitAndFinalizeRequest,
): Promise<CommitAndFinalizeResponse> {
  return request(`/runs/${encodeURIComponent(runId)}/commit-and-finalize`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}
