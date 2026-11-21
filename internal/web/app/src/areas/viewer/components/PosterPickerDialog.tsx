import { Check } from "lucide-react";
import { type RefObject, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { candidateProvenance, isRelativePreviewURL } from "@/lib/artworkCandidate";
import { fetchArtworkCandidates, pinArtworkCandidate } from "@/lib/graphql";
import type { ArtworkCandidate } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { artworkBucketURL } from "../lib/artwork";

// Quick poster picker on the detail page (CORE-2933). The same candidates and
// pin mutation the Manage artwork screen uses (ADR-0009): no second artwork
// path. It mounts only when opened, so the page loads no candidates until then.
export function PosterPickerDialog({
  mediaID,
  title,
  returnFocus,
  onClose,
  onChanged,
}: {
  mediaID: string;
  title: string;
  /** The control that opened the dialog; focus goes back to it on close. */
  returnFocus: RefObject<HTMLElement | null>;
  onClose: () => void;
  /** Called after a pin succeeds, before the dialog closes. */
  onChanged: () => void;
}) {
  const t = useT();
  // undefined = loading, null = unavailable.
  const [candidates, setCandidates] = useState<ArtworkCandidate[] | null | undefined>(undefined);
  const [pinning, setPinning] = useState<string | null>(null);
  const [pinFailed, setPinFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    setCandidates(undefined);
    fetchArtworkCandidates(mediaID, "poster")
      .then((list) => {
        if (alive) setCandidates(list);
      })
      .catch(() => {
        if (alive) setCandidates(null);
      });
    return () => {
      alive = false;
    };
  }, [mediaID]);

  async function choose(candidate: ArtworkCandidate) {
    if (candidate.applied) {
      onClose();
      return;
    }
    setPinning(candidate.id);
    setPinFailed(false);
    try {
      await pinArtworkCandidate(mediaID, "poster", candidate.id);
      onChanged();
      onClose();
    } catch {
      setPinFailed(true);
      setPinning(null);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(next) => {
        // A pin in flight keeps the dialog open, so its result can only close
        // this dialog and never a later one.
        if (!next && pinning === null) onClose();
      }}
    >
      <DialogContent className="bf-poster-picker sm:max-w-2xl" finalFocus={returnFocus}>
        <DialogHeader>
          <DialogTitle>{t("mediaDetailChoosePosterTitle")}</DialogTitle>
          <DialogDescription>{t("mediaDetailChoosePosterLead", { title })}</DialogDescription>
        </DialogHeader>
        {candidates === undefined && (
          <p role="status" className="bf-poster-picker-note">
            {t("loading")}
          </p>
        )}
        {candidates === null && (
          <p role="alert" className="bf-poster-picker-note" data-tone="danger">
            {t("artworkCandidatesUnavailable")}
          </p>
        )}
        {candidates?.length === 0 && (
          <p role="status" className="bf-poster-picker-note">
            {t("artworkCandidatesEmpty")}
          </p>
        )}
        {candidates && candidates.length > 0 && (
          <ul className="bf-poster-picker-grid">
            {candidates.map((candidate, index) => {
              const n = index + 1;
              const source = candidateProvenance(candidate);
              return (
                <li key={candidate.id}>
                  <button
                    type="button"
                    className="bf-poster-picker-option"
                    aria-current={candidate.applied ? "true" : undefined}
                    aria-busy={pinning === candidate.id ? "true" : undefined}
                    disabled={pinning !== null}
                    aria-label={
                      source
                        ? t("mediaDetailUsePosterFrom", { n, source })
                        : t("mediaDetailUsePosterNumber", { n })
                    }
                    onClick={() => void choose(candidate)}
                  >
                    <span className="bf-poster-picker-art">
                      {isRelativePreviewURL(candidate.previewURL) ? (
                        <img
                          src={artworkBucketURL(candidate.previewURL, "w320")}
                          alt=""
                          loading="lazy"
                          decoding="async"
                        />
                      ) : (
                        <span className="bf-poster-picker-missing">
                          {t("artworkPreviewUnavailable")}
                        </span>
                      )}
                      {candidate.applied && (
                        <span className="bf-poster-picker-applied">
                          <Check aria-hidden />
                          {t("artworkApplied")}
                        </span>
                      )}
                    </span>
                    {source && (
                      <span className="bf-poster-picker-source" title={source}>
                        {source}
                      </span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {pinFailed && (
          <p role="alert" className="bf-poster-picker-note" data-tone="danger">
            {t("couldNotPinArtworkCandidate")}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={pinning !== null}>
            {t("cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
