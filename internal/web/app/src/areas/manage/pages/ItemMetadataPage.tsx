import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { MediaMetadataEditor } from "@/components/beamfall/MediaMetadataEditor";
import { EmptyLine, ManageSection } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useI18n } from "@/lib/i18n";

// Per-item metadata editor page (CORE-1715): a media ID box over the shared
// MediaMetadataEditor, which the Media detail page's Edit details dialog also
// uses (CORE-2905). Matching an unidentified item stays with Fix Match.
export function ItemMetadataPage() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const params = useParams<{ mediaID?: string }>();
  const mediaID = params.mediaID ?? "";
  const [idInput, setIdInput] = useState(mediaID);

  useEffect(() => {
    setIdInput(mediaID);
  }, [mediaID]);

  const onOpen = () => {
    const id = idInput.trim();
    if (id) navigate(`/manage/items/${encodeURIComponent(id)}`);
  };

  return (
    <ManageSection>
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          onOpen();
        }}
      >
        <div className="space-y-1">
          <Label htmlFor="item-metadata-media">{t("fieldLocksMediaLabel")}</Label>
          <Input
            id="item-metadata-media"
            value={idInput}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setIdInput(e.target.value)}
            className="w-72"
          />
        </div>
        <Button type="submit" variant="outline" disabled={!idInput.trim()}>
          {t("fieldLocksLoad")}
        </Button>
      </form>

      <div className="mt-4">
        {mediaID ? (
          <MediaMetadataEditor key={mediaID} mediaID={mediaID} />
        ) : (
          <EmptyLine message={t("itemMetadataEmpty")} />
        )}
      </div>
    </ManageSection>
  );
}
