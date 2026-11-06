/** filesCutNotice is the sentence under a file list the server cut at its cap
 *  (GET /api/files `truncated`): how many rows are shown, of how many when the
 *  server said, and how to reach the rest. Used by every screen that lists the
 *  project's files, so none of them silently hides what the cap dropped. */
export function filesCutNotice(shown: number, total?: number): string {
  const of = total && total > shown ? ` of ${total}` : "";
  return (
    `Showing the first ${shown} files${of}. Open a file by its path for any other, ` +
    "or raise ITERION_STUDIO_FILES_LIMIT (0 lifts the cap)."
  );
}
