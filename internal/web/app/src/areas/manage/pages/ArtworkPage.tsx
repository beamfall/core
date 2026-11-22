import { useRef, useState } from "react";
import { EntityPicker } from "@/components/beamfall/EntityPicker";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { isRelativePreviewURL, candidateProvenance as provenance } from "@/lib/artworkCandidate";
import { fetchArtworkCandidates, pinArtworkCandidate } from "@/lib/graphql";
import type { ArtworkCandidate } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

const SLOTS: { slot: string; key: StringKey }[] = [
  { slot: "poster", key: "artworkSlotPoster" },
  { slot: "backdrop", key: "artworkSlotBackdrop" },
  { slot: "thumb", key: "artworkSlotThumb" },
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
      setError(t("artworkCandidatesUnavailable"));
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

  const onPin = async (slot: string, candidate: ArtworkCandidate) => {
    setError("");
    try {
      await pinArtworkCandidate(loadedID, slot, candidate.id);
      // Re-read the slot so `applied` reflects the server's confirmed state.
      await loadSlot(loadedID, slot);
    } catch {
      setError(t("couldNotPinArtworkCandidate"));
    }
  };

  return (
    <ManageSection>
      <div className="flex flex-wrap items-start gap-3">
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
              }
            }}
          />
        </div>
        <Button
          type="button"
          variant="outline"
          className="mt-6"
          onClick={() => void onLoad()}
          disabled={!mediaID.trim()}
        >
          {t("fieldLocksLoad")}
        </Button>
      </div>

      {loadedID && (
        <div className="mt-4 space-y-4">
          {SLOTS.map(({ slot, key }) => (
            <ArtworkSlot
              key={slot}
              title={t(key)}
              candidates={slots[slot]}
              onPin={(candidate) => onPin(slot, candidate)}
            />
          ))}
        </div>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}

function ArtworkSlot({
  title,
  candidates,
  onPin,
}: {
  title: string;
  candidates: ArtworkCandidate[] | null | undefined;
  onPin: (candidate: ArtworkCandidate) => void;
}) {
  const t = useT();
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
        <div role="list" className="grid grid-cols-[repeat(auto-fill,minmax(8rem,1fr))] gap-3">
          {candidates.map((candidate) => (
            <div role="listitem" key={candidate.id} className="space-y-1">
              <div
                data-applied={candidate.applied ? "true" : "false"}
                className="manage-art-tile flex aspect-[2/3] items-center justify-center overflow-hidden"
              >
                {isRelativePreviewURL(candidate.previewURL) ? (
                  <img src={candidate.previewURL} alt="" className="h-full w-full object-cover" />
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
                  onClick={() => onPin(candidate)}
                >
                  {t("pinArtworkCandidate")}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
