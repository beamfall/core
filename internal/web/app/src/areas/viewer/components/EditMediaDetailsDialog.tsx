import { MediaMetadataEditor } from "@/components/beamfall/MediaMetadataEditor";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useT } from "@/lib/i18n";

// Edit details from the Media detail page's More menu (CORE-2905): the shared
// metadata editor in a dialog, for the admin only. It mounts only when chosen,
// so the detail page loads nothing for it until then.
export function EditMediaDetailsDialog({
  mediaID,
  title,
  artwork,
  onChanged,
  onClose,
}: {
  mediaID: string;
  title: string;
  artwork?: Partial<Record<string, string>>;
  onChanged?: () => void;
  onClose: () => void;
}) {
  const t = useT();
  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("itemMetadataDialogTitle", { title })}</DialogTitle>
          <DialogDescription>{t("itemMetadataLead")}</DialogDescription>
        </DialogHeader>
        <MediaMetadataEditor mediaID={mediaID} currentArtwork={artwork} onChanged={onChanged} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("itemMetadataDialogClose")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
