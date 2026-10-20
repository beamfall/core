import { useState } from "react";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { fetchArtworkCandidates, pinArtworkCandidate } from "@/lib/graphql";
import type { ArtworkCandidate } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

const SLOTS: { slot: string; key: StringKey }[] = [
  { slot: "poster", key: "artworkSlotPoster" },
  { slot: "backdrop", key: "artworkSlotBackdrop" },
  { slot: "thumb", key: "artworkSlotThumb" },
];

// Only relative same-origin preview URLs render an <img> (app.js
// isRelativePreviewURL) — artwork bytes are served by the server at
// /api/media/artwork-candidate/...; an absolute or protocol-relative URL is
// never followed.
function isRelativePreviewURL(value: string | undefined): value is string {
  return typeof value === "string" && value.startsWith("/") && !value.startsWith("//");
}

function provenance(candidate: ArtworkCandidate): string {
  return [candidate.pluginID, candidate.scraperID].filter(Boolean).join(" / ");
}

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

  const loadSlot = async (id: string, slot: string) => {
    try {
      const candidates = await fetchArtworkCandidates(id, slot);
      setSlots((prev) => ({ ...prev, [slot]: candidates }));
    } catch {
      setSlots((prev) => ({ ...prev, [slot]: null }));
      setError(t("artworkCandidatesUnavailable"));
    }
  };

  const onLoad = async () => {
    setError("");
    const id = mediaID.trim();
    if (!id) return;
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
    <ManageSection title={t("artworkCandidates")}>
      <div className="flex items-end gap-3">
        <div className="space-y-1">
          <Label htmlFor="artwork-media">{t("fieldLocksMediaLabel")}</Label>
          <Input
            id="artwork-media"
            value={mediaID}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setMediaID(e.target.value)}
            className="w-72"
          />
        </div>
        <Button type="button" variant="outline" onClick={onLoad} disabled={!mediaID.trim()}>
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
