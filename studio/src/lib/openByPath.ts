// Opening a project file by the path the author types or pastes.
//
// The editor's file lists show only the project's own bots (bundles from
// bots/, examples/ and .botz/, and the .bot files in the project folder). A
// workflow kept anywhere else in the project — workflows/, docs/, tests/ — is
// opened by its path: the editor binds the tab to that path, so Save and Run
// work on the file itself, not on a copy. The server decides what a path may
// reach (it refuses a path outside the project folder); this helper only turns
// what a person types into the workspace-relative form the open route takes,
// and refuses what can never open early, with a sentence that says why.

import { workflowFileKind } from "@/lib/workflowFileKind";

export type OpenByPathResult = { ok: true; path: string } | { ok: false; error: string };

/** toSlashes turns a Windows spelling into the slash form the API uses. */
function toSlashes(p: string): string {
  return p.replace(/\\/g, "/");
}

/** isAbsolute: a POSIX root or a Windows drive. */
function isAbsolute(p: string): boolean {
  return p.startsWith("/") || /^[A-Za-z]:\//.test(p);
}

/** normalizeWorkflowPath returns the path to open: relative to the project
 *  folder (workDir, when the server told it) in slash form. An absolute path
 *  inside the project folder is made relative — a drive-letter path compares
 *  without regard to case, as the file system does. */
export function normalizeWorkflowPath(input: string, workDir?: string): OpenByPathResult {
  let p = input.trim();
  // A path pasted from a shell or a file manager often arrives quoted.
  if (p.length >= 2 && (p[0] === '"' || p[0] === "'") && p[p.length - 1] === p[0]) {
    p = p.slice(1, -1).trim();
  }
  if (p === "") return { ok: false, error: "Type the path of a .bot file." };
  p = toSlashes(p);
  while (p.startsWith("./")) p = p.slice(2);

  const kind = workflowFileKind(p);
  if (kind === "archive") {
    return {
      ok: false,
      error: "A .botz archive is installed, not opened: use Bots → Import → From a .botz file.",
    };
  }
  if (kind !== "bot") return { ok: false, error: "Only .bot files open in the editor." };

  if (isAbsolute(p) && workDir) {
    const root = toSlashes(workDir).replace(/\/+$/, "");
    const windowsLike = /^[A-Za-z]:$/.test(root.slice(0, 2));
    const a = windowsLike ? p.toLowerCase() : p;
    const b = windowsLike ? root.toLowerCase() : root;
    if (a.startsWith(`${b}/`)) return { ok: true, path: p.slice(root.length + 1) };
    return { ok: false, error: "That file is outside the project folder, and the editor opens project files only." };
  }
  return { ok: true, path: p };
}
