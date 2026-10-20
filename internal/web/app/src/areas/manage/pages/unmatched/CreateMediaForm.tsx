import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getIdentityEpochSignal } from "@/lib/api";
import { createMediaFromUnmatched } from "@/lib/graphql";
import type { UnmatchedRecordInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";

/** The earliest plausible release year, per the approved mockup's rule. */
const EARLIEST_YEAR = 1888;

/** Core's primary artwork slot (internal/artwork.SlotPoster) — matches the
 * literal already used at ArtworkPage.tsx/artwork.ts for the same slot. */
const ARTWORK_SLOT = "poster";

/**
 * uploadArtwork POSTs the selected file to the multipart create-Media artwork
 * route (METAPARITY-6-ARTWORK-1, internal/server/artwork_upload.go). It
 * duplicates lib/api.ts's CSRF-header + same-origin-credentials contract
 * in-line rather than importing lib/api.ts, because this ticket's Allowed
 * Paths are scoped to this directory only.
 */
async function uploadArtwork(
  mediaID: string,
  slot: string,
  file: File,
  signal?: AbortSignal,
): Promise<void> {
  const form = new FormData();
  form.append("mediaID", mediaID);
  form.append("slot", slot);
  form.append("file", file);
  const response = await fetch("/api/media/artwork/upload", {
    method: "POST",
    credentials: "same-origin",
    headers: { "X-Beamfall-Csrf": "1" },
    body: form,
    signal,
  });
  if (response.ok) return;
  const body = (await response.json().catch(() => null)) as { error?: string } | null;
  throw new Error(body?.error || `artwork upload failed (${response.status})`);
}

/**
 * validateYear enforces the mockup's stated rule: four digits between 1888 and
 * next year, or blank. It returns the parsed year, or null when the field is
 * unusable — the caller distinguishes blank (valid, omitted) from invalid.
 */
function validateYear(raw: string): { year?: number; invalid: boolean } {
  const text = raw.trim();
  if (text === "") return { invalid: false };
  const year = Number(text);
  const latest = new Date().getFullYear() + 1;
  if (!/^\d{4}$/.test(text) || year < EARLIEST_YEAR || year > latest) {
    return { invalid: true };
  }
  return { year, invalid: false };
}

/**
 * CreateMediaForm is METAPARITY-6 (ADR-0068 associate-vs-create): the operator
 * types the identity themselves and gets a normal browsable/playable Media with
 * NO external ID. Realizes the approved `fix-match-create-media` mockup —
 * required title, optional year, an explicit "no external ID" statement, and a
 * save failure that leaves the Unmatched record open with the entered values
 * retained for a retry.
 */
export function CreateMediaForm({
  record,
  libraryID,
  onCreated,
}: {
  record: UnmatchedRecordInfo;
  libraryID: string;
  onCreated: (title: string) => void;
}) {
  const t = useT();
  const [title, setTitle] = useState(record.candidateTitle || "");
  const [yearText, setYearText] = useState("");
  const [artworkFile, setArtworkFile] = useState<File | null>(null);
  const [titleError, setTitleError] = useState("");
  const [yearError, setYearError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [busy, setBusy] = useState(false);
  // The mockup's `artwork-error` state: the Media is already created (its ID
  // and title are pinned here) and the operator retries or omits artwork
  // without losing any entered field or the record's source file.
  const [artworkError, setArtworkError] = useState<{
    mediaID: string;
    title: string;
    message: string;
  } | null>(null);
  const retryFileInputRef = useRef<HTMLInputElement>(null);
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  /**
   * The generation captured for one operator submission: the identity epoch in
   * force when it started, plus this form's mounted lifetime. EVERY write below
   * is gated on it, not just the success write — a prior-epoch AbortError is
   * still a prior-epoch outcome, and rendering its message would put the
   * departed identity's failure on the next identity's screen.
   */
  const captureGeneration = () => {
    const epochSignal = getIdentityEpochSignal();
    return {
      epochSignal,
      current: () => !epochSignal?.aborted && mountedRef.current,
    };
  };

  const onSubmit = async () => {
    const { epochSignal, current } = captureGeneration();
    const trimmedTitle = title.trim();
    const parsed = validateYear(yearText);
    setTitleError(trimmedTitle ? "" : t("unmatchedCreateTitleRequired"));
    setYearError(parsed.invalid ? t("unmatchedCreateYearInvalid") : "");
    // Validation writes nothing and keeps every entered value available for
    // correction — the mockup's `validation-error` state.
    if (!trimmedTitle || parsed.invalid) return;

    setBusy(true);
    setSaveError("");
    try {
      const media = await createMediaFromUnmatched({
        recordID: record.id,
        libraryID,
        title: trimmedTitle,
        year: parsed.year,
      });
      if (artworkFile) {
        try {
          await uploadArtwork(media.id, ARTWORK_SLOT, artworkFile, epochSignal);
        } catch (err) {
          // A prior-epoch abort is not an artwork failure: the upload was
          // cancelled because the identity that started it is gone.
          if (!current()) return;
          // Media already exists (browsable/playable with fallback artwork);
          // only the artwork step failed, so drop into `artwork-error`
          // instead of the `save-error` path above.
          setArtworkError({
            mediaID: media.id,
            title: trimmedTitle,
            message: err instanceof Error ? err.message : "unsupported image",
          });
          return;
        }
      }
      if (!current()) return;
      onCreated(trimmedTitle);
    } catch {
      if (!current()) return;
      // The mockup's `save-error` state: no partial Media exists, the record
      // stays open, and the retry is safe because the Media ID is derived.
      setSaveError(t("unmatchedCreateFailed"));
    } finally {
      if (current()) setBusy(false);
    }
  };

  const retryArtwork = async (file: File) => {
    if (!artworkError) return;
    // Same captured generation as onSubmit: the retry runs on the Media created
    // by a specific identity, so an epoch change (or an unmount) mid-upload
    // invalidates every one of its outcomes, success and failure alike.
    const { epochSignal, current } = captureGeneration();
    setBusy(true);
    try {
      await uploadArtwork(artworkError.mediaID, ARTWORK_SLOT, file, epochSignal);
      if (!current()) return;
      onCreated(artworkError.title);
    } catch (err) {
      if (!current()) return;
      setArtworkError({
        ...artworkError,
        message: err instanceof Error ? err.message : "unsupported image",
      });
      if (retryFileInputRef.current) retryFileInputRef.current.value = "";
    } finally {
      if (current()) setBusy(false);
    }
  };

  if (artworkError) {
    return (
      <div className="grid gap-3 rounded-md border border-destructive bg-card p-3">
        <div>
          <h4 className="text-sm font-semibold text-foreground">
            {t("unmatchedCreateArtworkErrorHeading")}
          </h4>
        </div>
        <p role="alert" className="text-xs text-destructive">
          {artworkError.message}
        </p>
        <p className="text-xs text-muted-foreground">{t("unmatchedCreateArtworkErrorNotice")}</p>
        <input
          ref={retryFileInputRef}
          type="file"
          accept="image/jpeg,image/png"
          className="hidden"
          aria-label={t("unmatchedCreateArtworkUpload")}
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) void retryArtwork(file);
          }}
        />
        <div className="flex gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => retryFileInputRef.current?.click()}
          >
            {t("unmatchedCreateArtworkChooseAnother")}
          </Button>
          <Button size="sm" disabled={busy} onClick={() => onCreated(artworkError.title)}>
            {t("unmatchedCreateArtworkContinueWithout")}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="grid gap-3 rounded-md border border-border bg-card p-3">
      <div>
        <h4 className="text-sm font-semibold text-foreground">{t("unmatchedCreateHeading")}</h4>
        <p className="text-xs text-muted-foreground">{t("unmatchedCreateLead")}</p>
      </div>
      <p className="text-xs font-medium text-foreground">{t("unmatchedCreateNoExternalID")}</p>
      <div className="grid gap-1">
        <label className="text-xs text-muted-foreground" htmlFor="unmatched-create-title">
          {t("unmatchedCreateTitleLabel")}
        </label>
        <Input
          id="unmatched-create-title"
          value={title}
          aria-invalid={titleError !== ""}
          aria-describedby={titleError ? "unmatched-create-title-error" : undefined}
          onChange={(e) => setTitle(e.target.value)}
        />
        {titleError && (
          <p id="unmatched-create-title-error" role="alert" className="text-xs text-destructive">
            {titleError}
          </p>
        )}
      </div>
      <div className="grid gap-1">
        <label className="text-xs text-muted-foreground" htmlFor="unmatched-create-year">
          {t("unmatchedCreateYearLabel")}
        </label>
        <Input
          id="unmatched-create-year"
          inputMode="numeric"
          value={yearText}
          aria-invalid={yearError !== ""}
          aria-describedby={yearError ? "unmatched-create-year-error" : undefined}
          onChange={(e) => setYearText(e.target.value)}
        />
        {yearError && (
          <p id="unmatched-create-year-error" role="alert" className="text-xs text-destructive">
            {yearError}
          </p>
        )}
      </div>
      <div className="grid gap-1">
        <label className="text-xs text-muted-foreground" htmlFor="unmatched-create-artwork">
          {t("unmatchedCreateArtworkLabel")}
        </label>
        <Input
          id="unmatched-create-artwork"
          type="file"
          accept="image/jpeg,image/png"
          aria-label={t("unmatchedCreateArtworkUpload")}
          // Locked while the create is in flight: onSubmit's suspended handler
          // holds the `artworkFile` captured by the render that started it, so a
          // selection changed mid-flight would render one file and upload the
          // stale one (METAPARITY-ARTWORK-INPUT-RACE-1).
          disabled={busy}
          onChange={(e) => setArtworkFile(e.target.files?.[0] ?? null)}
        />
      </div>
      <p className="text-xs text-muted-foreground">{t("unmatchedCreateFilePreserved")}</p>
      {saveError && (
        <p role="alert" className="text-xs text-destructive">
          {saveError}
        </p>
      )}
      <Button size="sm" disabled={busy} onClick={() => void onSubmit()}>
        {t("unmatchedCreateSubmit")}
      </Button>
    </div>
  );
}
