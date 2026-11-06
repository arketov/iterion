/** NoBotsHint is the one sentence a bot picker or list shows when the project
 *  has no bots: it names the way forward instead of repeating the empty list.
 *  On a cloud server bots come from the team's catalog (no folder to put a
 *  bundle in); locally a bundle can also be dropped into bots/. */
export function NoBotsHint({ cloud = false }: { cloud?: boolean }) {
  return (
    <>
      No bots available. Create or import one on the Bots page
      {cloud ? "." : (
        <>
          , or put a bundle in the project&apos;s <code>bots/</code> folder.
        </>
      )}
    </>
  );
}
