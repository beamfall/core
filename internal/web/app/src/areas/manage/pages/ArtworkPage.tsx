import { useRef, useState } from "react";
import { Link } from "react-router";
import { EntityPicker } from "@/components/beamfall/EntityPicker";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { isRelativePreviewURL, candidateProvenance as provenance } from "@/lib/artworkCandidate";
import { fetchArtworkCandidates, pinArtworkCandidate, setMediaFieldLock } from "@/lib/graphql";
import type { ArtworkCandidate } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

const SLOTS: { slot: string; lock: string; key: StringKey; landscape: boolean }[] = [
  { slot: "poster", lock: "artworkPoster", key: "artworkSlotPoster", landscape: false },
  { slot: "backdrop", lock: "artworkBackdrop", key: "artworkSlotBackdrop", landscape: true },
  { slot: "thumb", lock: "artworkThumb", key: "artworkSlotThumb", landscape: true },
];

// Artwork candidates (ADR-0009): the candidate artwork per Media slot. Loaded by
// Media ID across the three core slots (poster / backdrop / thumb);
// pinArtworkCandidate marks one as applied. Ported from app.js
// refreshArtworkSlot / pinArtworkCandidate (the dialog is inlined here as a
// standalone manage route).
export function ArtworkPage() {
  const t = useT();
  const [mediaID, setMediaID] = useState("");
  const [loadedID, setLoadedID] = useState("");
  // undefined = loading, null = unavailable, array = candidates.
  const [slots, setSlots] = useState<Record<string, ArtworkCandidate[] | null | undefined>>({});
  const [pinningID, setPinningID] = useState("");
  const [error, setError] = useState("");
  // Only slots from the newest load may land, so a slow earlier pick cannot
  // fill the grid under a different Media ID.
  const loadSeq = useRef(0);

  const loadSlot = async (id: string, slot: string) => {
    const seq = loadSeq.current;
    try {
      const candidates = await fetchArtworkCandidates(id, slot);
      if (seq === loadSeq.current) setSlots((prev) => ({ ...prev, [slot]: candidates }));
    } catch {
      if (seq !== loadSeq.current) return;
      setSlots((prev) => ({ ...prev, [slot]: null }));
    }
  };

  const onLoad = async (picked?: string) => {
    setError("");
    const id = (picked ?? mediaID).trim();
    if (!id) return;
    loadSeq.current++;
    setLoadedID(id);
    setSlots({}); // every slot reads as undefined → "loading" until each lands
    await Promise.all(SLOTS.map(({ slot }) => loadSlot(id, slot)));
  };

  const onPin = async (slot: string, lock: string, candidate: ArtworkCandidate) => {
    setError("");
    setPinningID(candidate.id);
    try {
      await pinArtworkCandidate(loadedID, slot, candidate.id);
      if (typeof setMediaFieldLock === "function") {
        await setMediaFieldLock(loadedID, lock, true).catch(() => {});
      }
      // Re-read the slot so `applied` reflects the server's confirmed state.
      await loadSlot(loadedID, slot);
    } catch {
      setError(t("couldNotPinArtworkCandidate"));
    } finally {
      setPinningID("");
    }
  };

  return (
    <ManageSection>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-1">
            <Label htmlFor="artwork-media">{t("fieldLocksMediaLabel")}</Label>
            <EntityPicker
              id="artwork-media"
              value={mediaID}
              onChange={(id) => {
                setMediaID(id);
                if (id) void onLoad(id);
                else {
                  loadSeq.current++;
                  setLoadedID("");
                  setSlots({});
                  setError("");
                }
              }}
            />
          </div>
          <Button
            type="button"
            variant="outline"
            onClick={() => void onLoad()}
            disabled={!mediaID.trim()}
          >
            {t("fieldLocksLoad")}
          </Button>
        </div>
        {loadedID && (
          <Button asChild variant="outline" size="sm">
            <Link to={`/manage/items/${encodeURIComponent(loadedID)}`}>
              {t("itemMetadataHeading")}
            </Link>
          </Button>
        )}
      </div>

      <ErrorLine message={error} />

      {!loadedID ? (
        <div className="mt-4">
          <EmptyLine message={t("manageSelectMediaPrompt")} />
        </div>
      ) : (
        <div className="mt-4 space-y-6">
          {SLOTS.map(({ slot, lock, key, landscape }) => (
            <ArtworkSlot
              key={`${loadedID}:${slot}`}
              title={t(key)}
              landscape={landscape}
              candidates={slots[slot]}
              pinningID={pinningID}
              onPin={(candidate) => void onPin(slot, lock, candidate)}
            />
          ))}
        </div>
      )}
    </ManageSection>
  );
}

function ArtworkSlot({
  title,
  landscape,
  candidates,
  pinningID,
  onPin,
}: {
  title: string;
  landscape: boolean;
  candidates: ArtworkCandidate[] | null | undefined;
  pinningID: string;
  onPin: (candidate: ArtworkCandidate) => void;
}) {
  const t = useT();
  const [broken, setBroken] = useState<ReadonlySet<string>>(() => new Set());
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-semibold">{title}</h3>
      {candidates === undefined ? (
        <EmptyLine message={t("loading")} />
      ) : candidates === null ? (
        <EmptyLine message={t("artworkCandidatesUnavailable")} />
      ) : candidates.length === 0 ? (
        <EmptyLine message={t("artworkCandidatesEmpty")} />
      ) : (
        <div
          role="list"
          className={
            landscape
              ? "grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-3"
              : "grid grid-cols-[repeat(auto-fill,minmax(8rem,1fr))] gap-3"
          }
        >
          {candidates.map((candidate) => (
            <div role="listitem" key={candidate.id} className="space-y-1">
              <div
                data-applied={candidate.applied ? "true" : "false"}
                className={`manage-art-tile flex ${landscape ? "aspect-video" : "aspect-[2/3]"} items-center justify-center overflow-hidden`}
              >
                {isRelativePreviewURL(candidate.previewURL) && !broken.has(candidate.id) ? (
                  <img
                    src={candidate.previewURL}
                    alt=""
                    className="h-full w-full object-cover"
                    onError={() => setBroken((prev) => new Set(prev).add(candidate.id))}
                  />
                ) : (
                  <span className="px-2 text-center text-[11px] leading-4 text-[var(--text-muted)]">
                    {t("artworkPreviewUnavailable")}
                  </span>
                )}
              </div>
              <p
                className="truncate text-[11px] leading-4 text-[var(--text-muted)]"
                title={provenance(candidate)}
              >
                {provenance(candidate) || t("none")}
              </p>
              {candidate.applied ? (
                <StatusBadge state="green">{t("artworkApplied")}</StatusBadge>
              ) : (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="w-full"
                  disabled={pinningID !== ""}
                  onClick={() => onPin(candidate)}
                >
                  {pinningID === candidate.id ? t("loading") : t("pinArtworkCandidate")}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
