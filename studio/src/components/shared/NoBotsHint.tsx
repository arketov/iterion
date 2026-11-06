/** NoBotsHint is the one sentence a bot picker or list shows when the project
 *  has no bots: it names the way forward instead of repeating the empty list.
 *  On a cloud server the catalog is git-managed (the Bots page offers the
 *  marketplace, not create or import); locally a bundle can also be dropped
 *  into bots/. */
export function NoBotsHint({ cloud = false }: { cloud?: boolean }) {
  return (
    <>
      {cloud ? (
        "No bots available. Browse the marketplace on the Bots page to find bots to run."
      ) : (
        <>
          No bots available. Create or import one on the Bots page, or put a bundle in the
          project&apos;s <code>bots/</code> folder.
        </>
      )}
    </>
  );
}
