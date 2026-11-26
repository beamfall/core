import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { AssignDialog } from "@/components/assign/AssignDialog";
import { formatOrdinal, ordinalLabels } from "@/components/assign/assignOrdinals";
import { EntityPicker } from "@/components/beamfall/EntityPicker";
import { MediaMetadataEditor } from "@/components/beamfall/MediaMetadataEditor";
import { EmptyLine, ErrorLine, ManageSection } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { getIdentityEpochSignal } from "@/lib/api";
import { clearMediaOrdinal } from "@/lib/graphql";
import type { MediaMetadataInfo } from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { clearViewerCache } from "@/lib/viewerCache";

// Per-item metadata editor (CORE-1715): wraps the shared MediaMetadataEditor
// with the route-based item picker, adult-manager AssignDialog, and operator
// ordinal readout/clear.
export function ItemMetadataPage() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const { session, isManager } = useSession();
  const params = useParams<{ mediaID?: string }>();
  const mediaID = params.mediaID ?? "";
  const [idInput, setIdInput] = useState(mediaID);
  const [info, setInfo] = useState<MediaMetadataInfo | null>(null);
  const [assignmentRevision, setAssignmentRevision] = useState(0);
  const [ordinalStatus, setOrdinalStatus] = useState("");
  const [ordinalError, setOrdinalError] = useState("");
  const [ordinalBusy, setOrdinalBusy] = useState(false);
  const activeMediaID = useRef("");

  useEffect(() => {
    activeMediaID.current = mediaID;
    setIdInput(mediaID);
    setInfo(null);
    setOrdinalStatus("");
    setOrdinalError("");
    return () => {
      activeMediaID.current = "";
    };
  }, [mediaID]);

  const canAssign = isManager && session?.profile === "adult" && info?.mediaID === mediaID;

  const onOpen = (picked?: string) => {
    const id = (picked ?? idInput).trim();
    if (id) navigate(`/manage/items/${encodeURIComponent(id)}`);
  };

  const onInfoLoaded = useCallback((next: MediaMetadataInfo | null) => {
    setInfo(next);
  }, []);

  const onClearOrdinal = async () => {
    if (!canAssign || ordinalBusy) return;
    const epoch = getIdentityEpochSignal();
    const sameIdentity = () => !epoch?.aborted && epoch === getIdentityEpochSignal();
    setOrdinalStatus("");
    setOrdinalError("");
    setOrdinalBusy(true);
    try {
      await clearMediaOrdinal(mediaID);
      if (!sameIdentity()) return;
      clearViewerCache();
      if (activeMediaID.current !== mediaID) return;
      setInfo((current) =>
        current?.mediaID === mediaID ? { ...current, operatorOrdinal: null } : current,
      );
      setOrdinalStatus(t("assignOrdinalCleared"));
    } catch {
      if (sameIdentity() && activeMediaID.current === mediaID) {
        setOrdinalError(t("assignClearOrdinalFailed"));
      }
    } finally {
      setOrdinalBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection>
        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-1">
            <Label htmlFor="item-metadata-media">{t("fieldLocksMediaLabel")}</Label>
            <EntityPicker
              id="item-metadata-media"
              value={idInput}
              onChange={(id) => {
                setIdInput(id);
                if (id) onOpen(id);
              }}
            />
          </div>
          <Button
            type="button"
            variant="outline"
            disabled={!idInput.trim()}
            onClick={() => onOpen()}
          >
            {t("fieldLocksLoad")}
          </Button>
        </div>

        <div className="mt-4">
          {!mediaID ? (
            <EmptyLine message={t("itemMetadataEmpty")} />
          ) : (
            <MediaMetadataEditor
              key={`${mediaID}:${assignmentRevision}`}
              mediaID={mediaID}
              lockArtworkOnPin
              onInfoLoaded={onInfoLoaded}
              detailsActions={
                canAssign && info ? (
                  <AssignDialog
                    key={mediaID}
                    selection={[{ mediaID, title: info.title, year: info.year }]}
                    onChanged={() => setAssignmentRevision((r) => r + 1)}
                  />
                ) : undefined
              }
            />
          )}
        </div>
      </ManageSection>

      {canAssign && info && (
        <ManageSection title={t("assignOrdinal")}>
          <section className="space-y-3" aria-labelledby="item-metadata-ordinal">
            <h3 id="item-metadata-ordinal" className="sr-only">
              {t("assignOrdinal")}
            </h3>
            {info.operatorOrdinal ? (
              <>
                <p>{formatOrdinal(info.operatorOrdinal, ordinalLabels([], t))}</p>
                <Button
                  type="button"
                  variant="outline"
                  disabled={ordinalBusy}
                  onClick={() => void onClearOrdinal()}
                >
                  {t("assignClearOrdinal")}
                </Button>
              </>
            ) : (
              <EmptyLine message={t("assignNoOperatorOrdinal")} />
            )}
            {ordinalStatus && (
              <p role="status" className="text-sm">
                {ordinalStatus}
              </p>
            )}
            <ErrorLine message={ordinalError} />
          </section>
        </ManageSection>
      )}
    </div>
  );
}
