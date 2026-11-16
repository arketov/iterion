import { Button, Dialog, Input } from "@/components/ui";

import { isListedSavePath, type DocumentSaveAsController } from "./useDocumentSaveAs";

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
      {controller.names && (
        <p className="mb-2 text-sm text-fg-default">
          <span className="font-medium">{controller.names.label}</span>
          {controller.names.id !== controller.names.label && (
            <span className="ml-2 italic text-fg-subtle">{controller.names.id}</span>
          )}
        </p>
      )}
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
      {!isListedSavePath(controller.fileName) && (
        <p className="mt-2 text-caption text-warning-fg" role="status">
          A file saved here won&apos;t appear in Project files or on the Bots page — only in Recents or Open by
          path. To keep it listed, save it as a bot in bots/&lt;name&gt;/main.bot, or directly in the project
          root.
        </p>
      )}
      {controller.error && (
        <p className="mt-2 text-caption text-danger-fg" role="alert">
          {controller.error}
        </p>
      )}
    </Dialog>
  );
}
