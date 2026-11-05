// Header-slot actions for the board. Schema/maintenance actions collapse
// behind one "Manage" menu so the header reads: one primary (create) + one
// secondary (manage).

import { Button } from "@/components/ui/Button";
import { DropdownMenu, DropdownMenuItem } from "@/components/ui/DropdownMenu";

export function BoardHeaderActions({
  onManageLabels,
  onManageFields,
  onAddColumn,
  onRefresh,
  onNewIssue,
}: {
  onManageLabels: () => void;
  onManageFields: () => void;
  onAddColumn: () => void;
  onRefresh: () => void;
  onNewIssue: () => void;
}) {
  return (
    <>
      <Button
        variant="secondary"
        size="sm"
        onClick={onRefresh}
        title="Re-read every card from disk (cards created or changed by the CLI or MCP)"
      >
        Refresh
      </Button>
      <DropdownMenu
        trigger={
          <Button variant="secondary" size="sm">
            Manage
          </Button>
        }
        align="end"
      >
        <DropdownMenuItem onSelect={onManageLabels}>Labels…</DropdownMenuItem>
        <DropdownMenuItem onSelect={onManageFields}>Fields…</DropdownMenuItem>
        <DropdownMenuItem onSelect={onAddColumn}>Add column…</DropdownMenuItem>
      </DropdownMenu>
      <Button variant="primary" size="sm" onClick={onNewIssue}>
        + New issue
      </Button>
    </>
  );
}
