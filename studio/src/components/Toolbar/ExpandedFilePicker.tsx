import { useUIStore } from "@/store/ui";
import { useConfirm } from "@/hooks/useConfirm";
import FilePicker from "../FilePicker/FilePicker";
import { useDocumentFileOps } from "./useDocumentFileOps";

// The file picker of the expanded editor. The picker is opened through the
// global `filePickerOpen` flag (the Canvas palette's "Open file…", the empty
// canvas tiles, the global palette), and its one renderer in a tab is the
// Toolbar — which the expanded layout does not mount. Without this the flag
// would stay raised with nothing to show it, and the picker would pop up
// later, wherever a renderer next mounts (a start page after a tab closes).
export default function ExpandedFilePicker() {
  const filePickerOpen = useUIStore((s) => s.filePickerOpen);
  const setFilePickerOpen = useUIStore((s) => s.setFilePickerOpen);
  const { confirm, dialog: confirmDialog } = useConfirm();
  const { handlePickFile } = useDocumentFileOps({ confirm });
  return (
    <>
      <FilePicker
        open={filePickerOpen}
        onOpenChange={setFilePickerOpen}
        onPick={handlePickFile}
      />
      {confirmDialog}
    </>
  );
}
