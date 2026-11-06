import { useEffect, useState } from "react";

import {
  getRelocationOffer,
  relocateProject,
  type RelocationSource,
} from "@/api/projects";
import { Button } from "@/components/ui/Button";
import { InlineBanner } from "@/components/ui/InlineBanner";
import { useAsyncAction } from "@/hooks/useAsyncAction";

interface RelocationOfferProps {
  projectId: string | undefined;
  // Called after the data was copied, so the view re-reads the dispatcher
  // status and the board.
  onMoved: () => void;
}

// A project's store is addressed by its folder's path, so a project moved to
// another folder opens with an empty board and no dispatcher settings while its
// data sits under the old path. When the server finds that old project (a
// registered one whose folder is gone, same folder name, store with data) and
// this project's own store is still empty, offer to copy the data over. The
// copy only happens on the click; the old data is left in place.
export default function RelocationOffer({ projectId, onMoved }: RelocationOfferProps) {
  const [sources, setSources] = useState<RelocationSource[]>([]);
  const { run, busy, error } = useAsyncAction();

  useEffect(() => {
    let cancelled = false;
    setSources([]);
    if (!projectId) return;
    getRelocationOffer(projectId)
      .then((offer) => {
        if (!cancelled && offer.destination_empty) setSources(offer.sources ?? []);
      })
      // An offer is a convenience: no offer is the same as a failed lookup.
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [projectId]);

  if (!projectId || sources.length === 0) return null;

  const move = (source: RelocationSource) =>
    void run(async () => {
      await relocateProject(projectId, source.id);
      setSources([]);
      onMoved();
    });

  return (
    <div className="flex flex-col items-center gap-2 text-fg-default">
      {sources.map((source) => (
        <InlineBanner
          key={source.id}
          tone="info"
          layout="inline"
          title="Data of this project found at its old location"
          action={
            <Button
              variant="primary"
              size="sm"
              disabled={busy}
              onClick={() => move(source)}
            >
              {`Move data from ${source.dir}`}
            </Button>
          }
        >
          {source.cards} cards, {source.runs} runs. Secrets, work copies and the
          dispatcher&apos;s running state are not moved; the dispatcher stays
          stopped. The old data is kept.
        </InlineBanner>
      ))}
      {error && <InlineBanner tone="danger">{error}</InlineBanner>}
    </div>
  );
}
