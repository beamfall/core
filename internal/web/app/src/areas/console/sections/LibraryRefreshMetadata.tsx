import { RefreshCcwDot } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { refreshLibraryMetadata } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { ConfirmButton } from "../ConfirmButton";

// "Refresh all metadata" for one Library (CORE-1136). The server runs it as a
// background job (deduped per Library, so a second click returns the job
// already running); its progress and Cancel live in Operations, so the card
// only confirms that it was queued and points there.
export function RefreshMetadataButton({ libraryID }: { libraryID: string }) {
  const t = useT();
  const [queued, setQueued] = useState(false);

  return (
    <div className="flex flex-wrap items-center gap-2">
      {queued && (
        <p role="status" className="text-xs text-muted-foreground">
          {t("refreshAllMetadataQueued")}{" "}
          <Link to="/console/operations" className="underline underline-offset-2">
            {t("refreshAllMetadataFollow")}
          </Link>
        </p>
      )}
      <ConfirmButton
        variant="outline"
        size="sm"
        confirm="confirmRefreshAllMetadata"
        onConfirm={async () => {
          await refreshLibraryMetadata(libraryID);
          setQueued(true);
        }}
        label={
          <>
            <RefreshCcwDot className="console-nav-icon" aria-hidden />
            {t("refreshAllMetadata")}
          </>
        }
        actionLabel={t("refreshAllMetadata")}
      >
        {t("refreshAllMetadataBody")}
      </ConfirmButton>
    </div>
  );
}
