import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { EmptyLine, ErrorLine, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { uploadMediaArtwork } from "@/lib/api";
import { displayString } from "@/lib/display";
import {
  bulkEditApply,
  editMediaMetadata,
  fetchArtworkCandidates,
  fetchMediaFields,
  fetchMediaMetadata,
  pinArtworkCandidate,
  refreshMediaMetadata,
  setMediaFieldLock,
} from "@/lib/graphql";
import type {
  ArtworkCandidate,
  EntityFieldInfo,
  FieldLockInfo,
  MediaMetadataEdit,
  MediaMetadataInfo,
} from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";
import type { StringKey } from "@/strings";

const SLOTS: { slot: string; lock: string; key: StringKey; landscape: boolean }[] = [
  { slot: "poster", lock: "artworkPoster", key: "artworkSlotPoster", landscape: false },
  { slot: "backdrop", lock: "artworkBackdrop", key: "artworkSlotBackdrop", landscape: true },
  { slot: "thumb", lock: "artworkThumb", key: "artworkSlotThumb", landscape: true },
];

const UPLOAD_ACCEPT = "image/jpeg,image/png";

interface Draft {
  title: string;
  sortTitle: string;
  summary: string;
  year: string;
}

function draftOf(info: MediaMetadataInfo): Draft {
  return {
    title: info.title,
    sortTitle: info.sortTitle,
    summary: info.summary,
    year: info.year > 0 ? String(info.year) : "",
  };
}

// Only relative same-origin preview URLs render an <img>, as on ArtworkPage.
function isRelativePreviewURL(value: string | undefined): value is string {
  return typeof value === "string" && value.startsWith("/") && !value.startsWith("//");
}

// A changed slot keeps its URL, so a version suffix makes the browser fetch
// the new image instead of showing its cached copy.
function versioned(url: string, version: number): string {
  if (version === 0) return url;
  return `${url}${url.includes("?") ? "&" : "?"}edit=${version}`;
}

export interface MediaMetadataEditorProps {
  mediaID: string;
  /** Current artwork URL per slot ("poster", "backdrop", "thumb"), shown beside the candidates. */
  currentArtwork?: Partial<Record<string, string>>;
  /** Called after any change is saved, so the host can reload what it shows. */
  onChanged?: () => void;
  /** Called whenever metadata finishes loading or updates. */
  onInfoLoaded?: (info: MediaMetadataInfo | null) => void;
  /** Explicitly lock the artwork slot via setMediaFieldLock when pinning a candidate. */
  lockArtworkOnPin?: boolean;
  /** Optional actions rendered beside the Details Save button. */
  detailsActions?: ReactNode;
}

// The per-item metadata editor (CORE-1715, CORE-2905), shared by the Manage
// item page and the Media detail page's Edit details dialog. Core details go
// through editMediaMetadata, plugin-declared fields through bulkEditApply and
// artwork through pinArtworkCandidate or an upload. Every edit is a
// manual-rank write, which is what a lock is: identification, refresh and
// rescans write below that rank and leave the field alone until it is
// unlocked. Revert to provider unlocks a field and, when the item has a
// provider match, refreshes it so the provider's value comes back now.
export function MediaMetadataEditor({
  mediaID,
  currentArtwork,
  onChanged,
  onInfoLoaded,
  lockArtworkOnPin = false,
  detailsActions,
}: MediaMetadataEditorProps) {
  const { t, displayLocale } = useI18n();
  // undefined = loading, null = unavailable.
  const [info, setInfo] = useState<MediaMetadataInfo | null | undefined>(undefined);
  const [draft, setDraft] = useState<Draft>({ title: "", sortTitle: "", summary: "", year: "" });
  const [fields, setFields] = useState<EntityFieldInfo[]>([]);
  const [fieldDraft, setFieldDraft] = useState<Record<string, string>>({});
  const [artwork, setArtwork] = useState<Record<string, ArtworkCandidate[] | null>>({});
  const [artworkVersion, setArtworkVersion] = useState(0);
  // Candidates whose preview failed to load (an upload made before previews
  // were cached has no fetchable source), so no empty frame is drawn for them.
  const [brokenPreviews, setBrokenPreviews] = useState<ReadonlySet<string>>(() => new Set());
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInputs = useRef<Record<string, HTMLInputElement | null>>({});
  const loadSeq = useRef(0);
  const onInfoLoadedRef = useRef(onInfoLoaded);
  onInfoLoadedRef.current = onInfoLoaded;

  const applyInfo = useCallback((next: MediaMetadataInfo) => {
    setInfo(next);
    setDraft(draftOf(next));
    onInfoLoadedRef.current?.(next);
  }, []);

  const loadFields = useCallback(async (id: string, seq?: number) => {
    try {
      const next = await fetchMediaFields(id);
      if (seq !== undefined && seq !== loadSeq.current) return;
      setFields(next);
      setFieldDraft(Object.fromEntries(next.map((f) => [f.name, f.value])));
    } catch {
      if (seq !== undefined && seq !== loadSeq.current) return;
      setFields([]);
      setFieldDraft({});
    }
  }, []);

  const loadSlot = useCallback(async (id: string, slot: string, seq?: number) => {
    try {
      const candidates = await fetchArtworkCandidates(id, slot);
      if (seq !== undefined && seq !== loadSeq.current) return;
      setArtwork((prev) => ({ ...prev, [slot]: candidates }));
    } catch {
      if (seq !== undefined && seq !== loadSeq.current) return;
      setArtwork((prev) => ({ ...prev, [slot]: null }));
    }
  }, []);

  const loadAll = useCallback(
    async (id: string, seq?: number) => {
      await Promise.all([loadFields(id, seq), ...SLOTS.map(({ slot }) => loadSlot(id, slot, seq))]);
    },
    [loadFields, loadSlot],
  );

  useEffect(() => {
    const seq = ++loadSeq.current;
    setStatus("");
    setError("");
    setArtwork({});
    setFields([]);
    setFieldDraft({});
    setDraft({ title: "", sortTitle: "", summary: "", year: "" });
    setBrokenPreviews(new Set());
    setBusy(false);
    setInfo(undefined);
    fetchMediaMetadata(mediaID)
      .then((next) => {
        if (seq === loadSeq.current) applyInfo(next);
      })
      .catch(() => {
        if (seq === loadSeq.current) {
          setInfo(null);
          onInfoLoadedRef.current?.(null);
        }
      });
    void loadAll(mediaID, seq);
    return () => {
      loadSeq.current += 1;
    };
  }, [mediaID, applyInfo, loadAll]);

  const locked = (field: string): boolean =>
    info?.locks.some((lock) => lock.field === field && lock.locked) ?? false;

  const setLocks = (locks: FieldLockInfo[]) => {
    setInfo((prev) => (prev ? { ...prev, locks } : prev));
  };

  // refreshLocks re-reads only the lock state, so an artwork or plugin-field
  // change never discards unsaved edits in the details form.
  const refreshLocks = async (seq: number) => {
    const next = await fetchMediaMetadata(mediaID);
    if (loadSeq.current !== seq) return;
    setLocks(next.locks);
  };

  const changed = () => {
    setArtworkVersion((v) => v + 1);
    onChanged?.();
  };

  const begin = () => {
    setStatus("");
    setError("");
  };

  const onToggleLock = async (field: string) => {
    if (busy) return;
    const seq = loadSeq.current;
    begin();
    setBusy(true);
    try {
      const next = await setMediaFieldLock(mediaID, field, !locked(field));
      if (loadSeq.current !== seq) return;
      setLocks(next);
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("itemMetadataLockFailed"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
    }
  };

  const onRevert = async (field: string) => {
    const seq = loadSeq.current;
    begin();
    setBusy(true);
    try {
      const nextLocks = await setMediaFieldLock(mediaID, field, false, true);
      if (loadSeq.current !== seq) return;
      setLocks(nextLocks);
      if (info?.refreshable) {
        const refreshed = await refreshMediaMetadata(mediaID);
        if (loadSeq.current !== seq) return;
        applyInfo(refreshed);
        await loadAll(mediaID, seq);
        if (loadSeq.current !== seq) return;
        setStatus(t("itemMetadataReverted"));
      } else {
        setStatus(t("itemMetadataRevertedUnlocked"));
      }
      changed();
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("itemMetadataRevertFailed"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
    }
  };

  const onSaveDetails = async () => {
    if (!info) return;
    begin();
    const edit: MediaMetadataEdit = {};
    if (draft.title !== info.title) {
      if (!draft.title.trim()) {
        setError(t("itemMetadataTitleRequired"));
        return;
      }
      edit.title = draft.title;
    }
    if (draft.sortTitle !== info.sortTitle) edit.sortTitle = draft.sortTitle;
    if (draft.summary !== info.summary) edit.summary = draft.summary;
    if (draft.year !== draftOf(info).year) {
      const year = draft.year.trim() === "" ? 0 : Number(draft.year);
      if (!Number.isInteger(year) || year < 0 || year > 9999) {
        setError(t("itemMetadataYearInvalid"));
        return;
      }
      edit.year = year;
    }
    if (Object.keys(edit).length === 0) {
      setStatus(t("itemMetadataNoChanges"));
      return;
    }
    const seq = loadSeq.current;
    setBusy(true);
    try {
      const saved = await editMediaMetadata(mediaID, edit);
      if (loadSeq.current !== seq) return;
      applyInfo(saved);
      setStatus(t("itemMetadataSaved"));
      onChanged?.();
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("itemMetadataSaveFailed"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
    }
  };

  const onSaveFields = async () => {
    begin();
    const edits = fields
      .filter((f) => (fieldDraft[f.name] ?? f.value) !== f.value)
      .map((f) => ({ name: f.name, type: f.type, value: fieldDraft[f.name] ?? "" }));
    if (edits.length === 0) {
      setStatus(t("itemMetadataNoChanges"));
      return;
    }
    const seq = loadSeq.current;
    setBusy(true);
    try {
      await bulkEditApply([mediaID], edits);
      if (loadSeq.current !== seq) return;
      await refreshLocks(seq);
      if (loadSeq.current !== seq) return;
      await loadFields(mediaID, seq);
      if (loadSeq.current !== seq) return;
      setStatus(t("itemMetadataSaved"));
      onChanged?.();
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("itemMetadataSaveFailed"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
    }
  };

  const onChooseArtwork = async (slot: string, lock: string, candidate: ArtworkCandidate) => {
    if (busy) return;
    const seq = loadSeq.current;
    begin();
    setBusy(true);
    try {
      await pinArtworkCandidate(mediaID, slot, candidate.id);
      if (loadSeq.current !== seq) return;
      if (lockArtworkOnPin) {
        const nextLocks = await setMediaFieldLock(mediaID, lock, true);
        if (loadSeq.current !== seq) return;
        setLocks(nextLocks);
      } else {
        await refreshLocks(seq);
        if (loadSeq.current !== seq) return;
      }
      await loadSlot(mediaID, slot, seq);
      if (loadSeq.current !== seq) return;
      changed();
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("couldNotPinArtworkCandidate"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
    }
  };

  const onUploadArtwork = async (slot: string, file: File | undefined) => {
    if (!file) return;
    const seq = loadSeq.current;
    begin();
    setBusy(true);
    try {
      await uploadMediaArtwork(mediaID, slot, file);
      if (loadSeq.current !== seq) return;
      await refreshLocks(seq);
      if (loadSeq.current !== seq) return;
      await loadSlot(mediaID, slot, seq);
      if (loadSeq.current !== seq) return;
      setStatus(t("itemMetadataArtworkUploaded"));
      changed();
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("itemMetadataArtworkUploadFailed"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
      const input = fileInputs.current[slot];
      if (input) input.value = "";
    }
  };

  const onRefresh = async () => {
    const seq = loadSeq.current;
    begin();
    setBusy(true);
    try {
      const refreshed = await refreshMediaMetadata(mediaID);
      if (loadSeq.current !== seq) return;
      applyInfo(refreshed);
      await loadAll(mediaID, seq);
      if (loadSeq.current !== seq) return;
      setStatus(t("itemMetadataRefreshed"));
      changed();
    } catch {
      if (loadSeq.current !== seq) return;
      setError(t("itemMetadataRefreshFailed"));
    } finally {
      if (loadSeq.current === seq) setBusy(false);
    }
  };

  const lockControl = (field: string, label: string) => {
    const isLocked = locked(field);
    return (
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge state={isLocked ? "green" : "neutral"}>
          {isLocked ? t("itemMetadataLockedBadge") : t("itemMetadataUnlockedBadge")}
        </StatusBadge>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          aria-label={t(isLocked ? "itemMetadataUnlockField" : "itemMetadataLockField", {
            field: label,
          })}
          onClick={() => onToggleLock(field)}
        >
          {isLocked ? t("fieldLockRelease") : t("fieldLockPin")}
        </Button>
        {isLocked ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            disabled={busy}
            aria-label={t("itemMetadataRevertField", { field: label })}
            onClick={() => onRevert(field)}
          >
            {t("itemMetadataRevert")}
          </Button>
        ) : null}
      </div>
    );
  };

  const detailRow = (
    field: keyof Draft,
    label: string,
    control: (id: string) => ReactNode,
    hint?: string,
  ) => {
    const id = `item-metadata-${field}`;
    return (
      <div className="space-y-1">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Label htmlFor={id}>{label}</Label>
          {lockControl(field, label)}
        </div>
        {control(id)}
        {hint ? <p className="text-muted-foreground text-xs">{hint}</p> : null}
      </div>
    );
  };

  const setDraftField = (field: keyof Draft) => (value: string) =>
    setDraft((prev) => ({ ...prev, [field]: value }));

  if (info === null) return <ErrorLine message={t("itemMetadataUnavailable")} />;
  if (info === undefined) return <Skeleton aria-hidden="true" className="h-64 rounded-xl" />;

  return (
    <div className="space-y-6">
      {status ? (
        <p role="status" className="text-sm font-medium">
          {status}
        </p>
      ) : null}
      <ErrorLine message={error} />

      <section className="space-y-3" aria-labelledby="item-metadata-details">
        <h3 id="item-metadata-details" className="font-medium">
          {t("itemMetadataDetailsHeading")}
        </h3>
        {detailRow("title", t("itemMetadataTitle"), (id) => (
          <Input
            id={id}
            value={draft.title}
            onChange={(e) => setDraftField("title")(e.target.value)}
          />
        ))}
        {detailRow(
          "sortTitle",
          t("itemMetadataSortTitle"),
          (id) => (
            <Input
              id={id}
              value={draft.sortTitle}
              onChange={(e) => setDraftField("sortTitle")(e.target.value)}
            />
          ),
          t("itemMetadataSortTitleHint"),
        )}
        {detailRow("year", t("itemMetadataYear"), (id) => (
          <Input
            id={id}
            inputMode="numeric"
            value={draft.year}
            onChange={(e) => setDraftField("year")(e.target.value)}
            className="w-32"
          />
        ))}
        {detailRow("summary", t("itemMetadataOverview"), (id) => (
          <Textarea
            id={id}
            value={draft.summary}
            onChange={(e) => setDraftField("summary")(e.target.value)}
          />
        ))}
        <div className="flex flex-wrap gap-2">
          <Button type="button" onClick={onSaveDetails} disabled={busy}>
            {t("itemMetadataSave")}
          </Button>
          {detailsActions}
        </div>
      </section>

      <section
        className="space-y-3 border-t border-[var(--border-subtle)] pt-4"
        aria-labelledby="item-metadata-fields"
      >
        <h3 id="item-metadata-fields" className="font-medium">
          {t("itemMetadataFieldsHeading")}
        </h3>
        <p className="text-muted-foreground text-sm">{t("itemMetadataFieldsLead")}</p>
        {fields.length === 0 ? (
          <EmptyLine message={t("itemMetadataFieldsEmpty")} />
        ) : (
          <>
            {fields.map((field) => {
              const id = `item-metadata-field-${field.name}`;
              const label = displayString(field.label, displayLocale) || field.name;
              return (
                <div key={field.name} className="space-y-1">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <Label htmlFor={id}>{label}</Label>
                    {lockControl(field.name, label)}
                  </div>
                  <Input
                    id={id}
                    value={fieldDraft[field.name] ?? ""}
                    onChange={(e) =>
                      setFieldDraft((prev) => ({ ...prev, [field.name]: e.target.value }))
                    }
                  />
                </div>
              );
            })}
            <Button type="button" onClick={onSaveFields} disabled={busy}>
              {t("itemMetadataSave")}
            </Button>
          </>
        )}
      </section>

      <section
        className="space-y-3 border-t border-[var(--border-subtle)] pt-4"
        aria-labelledby="item-metadata-artwork"
      >
        <h3 id="item-metadata-artwork" className="font-medium">
          {t("itemMetadataArtworkHeading")}
        </h3>
        <p className="text-muted-foreground text-sm">{t("itemMetadataArtworkLead")}</p>
        {SLOTS.map(({ slot, lock, key, landscape }) => {
          const candidates = artwork[slot];
          const current = currentArtwork?.[slot];
          const slotLabel = t(key);
          return (
            <div key={slot} className="space-y-2">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span className="text-sm font-medium">{slotLabel}</span>
                {lockControl(lock, slotLabel)}
              </div>
              <div className="flex flex-wrap items-end gap-3">
                {isRelativePreviewURL(current) ? (
                  <figure className="m-0 space-y-1">
                    <img
                      src={versioned(current, artworkVersion)}
                      alt={t("itemMetadataArtworkCurrentAlt", { slot: slotLabel })}
                      className="h-24 w-auto rounded-md border"
                    />
                    <figcaption className="text-muted-foreground text-xs">
                      {t("itemMetadataArtworkCurrent")}
                    </figcaption>
                  </figure>
                ) : null}
                <input
                  ref={(el) => {
                    fileInputs.current[slot] = el;
                  }}
                  type="file"
                  accept={UPLOAD_ACCEPT}
                  className="hidden"
                  aria-hidden="true"
                  tabIndex={-1}
                  data-testid={`item-metadata-upload-${slot}`}
                  onChange={(e) => void onUploadArtwork(slot, e.target.files?.[0])}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={busy}
                  aria-label={t("itemMetadataArtworkUploadSlot", { slot: slotLabel })}
                  onClick={() => fileInputs.current[slot]?.click()}
                >
                  {t("itemMetadataArtworkUpload")}
                </Button>
              </div>
              {!candidates || candidates.length === 0 ? (
                <EmptyLine message={t("itemMetadataArtworkNone")} />
              ) : (
                <ul className="flex flex-wrap gap-3">
                  {candidates.map((candidate) => (
                    <li key={candidate.id} className="space-y-1">
                      {isRelativePreviewURL(candidate.previewURL) &&
                      !brokenPreviews.has(candidate.id) ? (
                        <img
                          src={candidate.previewURL}
                          alt=""
                          className={`${landscape ? "aspect-video w-40" : "aspect-[2/3] w-20"} rounded-md border object-cover`}
                          onError={() =>
                            setBrokenPreviews((prev) => new Set(prev).add(candidate.id))
                          }
                        />
                      ) : null}
                      {candidate.applied ? (
                        <StatusBadge state="green">{t("itemMetadataArtworkApplied")}</StatusBadge>
                      ) : (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={busy}
                          onClick={() => onChooseArtwork(slot, lock, candidate)}
                        >
                          {t("itemMetadataArtworkChoose")}
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          );
        })}
      </section>

      <section
        className="space-y-3 border-t border-[var(--border-subtle)] pt-4"
        aria-labelledby="item-metadata-refresh"
      >
        <h3 id="item-metadata-refresh" className="font-medium">
          {t("itemMetadataRefreshHeading")}
        </h3>
        <p className="text-muted-foreground text-sm">
          {info.refreshable ? t("itemMetadataRefreshLead") : t("itemMetadataRefreshNeedsMatch")}
        </p>
        <Button
          type="button"
          variant="outline"
          onClick={onRefresh}
          disabled={busy || !info.refreshable}
        >
          {t("itemMetadataRefresh")}
        </Button>
      </section>
    </div>
  );
}
