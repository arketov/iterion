import { Button, Dialog, Input } from "@/components/ui";

import type { DocumentSaveAsController } from "./useDocumentSaveAs";

export default function DocumentSaveAsDialog({
  controller,
}: {
  controller: DocumentSaveAsController;
}) {
  return (
    <Dialog
      open={controller.open}
      onOpenChange={(open) => {
        if (open || !controller.busy) controller.setOpen(open);
      }}
      title="Save As"
      widthClass="max-w-sm"
      hideClose={controller.busy}
      footer={
        <>
          <Button
            variant="secondary"
            size="sm"
            disabled={controller.busy}
            onClick={() => controller.setOpen(false)}
          >
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={controller.busy || controller.fileName.trim() === ""}
            onClick={() => void controller.confirmSaveAs()}
          >
            {controller.busy ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <Input
        autoFocus
        value={controller.fileName}
        onChange={(event) => controller.setFileName(event.target.value)}
        placeholder="filename.bot"
        size="md"
        disabled={controller.busy}
        onKeyDown={(event) => {
          if (event.key === "Enter") void controller.confirmSaveAs();
          if (event.key === "Escape" && !controller.busy) controller.setOpen(false);
        }}
      />
      <p className="mt-2 text-caption text-fg-muted">
        Save As creates a new file under this name (it is not renamed to match the workflow) and writes it in
        iterion&apos;s canonical format: field order, commas and line breaks may differ from the original.
      </p>
      {controller.error && (
        <p className="mt-2 text-caption text-danger-fg" role="alert">
          {controller.error}
        </p>
      )}
    </Dialog>
  );
}
