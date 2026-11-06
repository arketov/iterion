// What a file name is to the editor: a workflow source it opens (`.bot`), a
// packed bundle it installs instead (`.botz`), or neither. One place, so the
// Open dialogs, the import box and the open-by-path field answer the same way.
// The server's own rule is pkg/dsl/workflowfile (extensions folded to lower
// case); `.botz` is a bundle archive, handled by the bundle loader and the
// Bots page's Import menu, never opened as a document.

export type WorkflowFileKind = "bot" | "archive" | "other";

export function workflowFileKind(name: string): WorkflowFileKind {
  const lower = name.toLowerCase();
  if (lower.endsWith(".botz")) return "archive";
  if (lower.endsWith(".bot")) return "bot";
  return "other";
}

/** refusalFor is the sentence shown when a file the author picked cannot be
 *  opened in the editor, or null when it can. */
export function refusalFor(name: string): string | null {
  switch (workflowFileKind(name)) {
    case "bot":
      return null;
    case "archive":
      return "A .botz archive is installed, not opened: use Bots → Import → From a .botz file.";
    default:
      return "Only .bot files can be opened";
  }
}
