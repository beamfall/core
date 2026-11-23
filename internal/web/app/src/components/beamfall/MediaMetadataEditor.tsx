import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { EmptyLine, ErrorLine, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

const SLOTS: { slot: string; lock: string; key: StringKey }[] = [
  { slot: "poster", lock: "artworkPoster", key: "artworkSlotPoster" },
  { slot: "backdrop", lock: "artworkBackdrop", key: "artworkSlotBackdrop" },
  { slot: "thumb", lock: "artworkThumb", key: "artworkSlotThumb" },
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

  const applyInfo = useCallback((next: MediaMetadataInfo) => {
    setInfo(next);
    setDraft(draftOf(next));
  }, []);

  const loadFields = useCallback(async (id: string) => {
    try {
      const next = await fetchMediaFields(id);
      setFields(next);
      setFieldDraft(Object.fromEntries(next.map((f) => [f.name, f.value])));
    } catch {
      setFields([]);
      setFieldDraft({});
    }
  }, []);

  const loadSlot = useCallback(async (id: string, slot: string) => {
    try {
      const candidates = await fetchArtworkCandidates(id, slot);
      setArtwork((prev) => ({ ...prev, [slot]: candidates }));
    } catch {
      setArtwork((prev) => ({ ...prev, [slot]: null }));
    }
  }, []);

  const loadAll = useCallback(
    async (id: string) => {
      await Promise.all([loadFields(id), ...SLOTS.map(({ slot }) => loadSlot(id, slot))]);
    },
    [loadFields, loadSlot],
  );

  useEffect(() => {
    setStatus("");
    setError("");
    setArtwork({});
    setFields([]);
    let cancelled = false;
    setInfo(undefined);
    fetchMediaMetadata(mediaID)
      .then((next) => {
        if (!cancelled) applyInfo(next);
      })
      .catch(() => {
        if (!cancelled) setInfo(null);
      });
    void loadAll(mediaID);
    return () => {
      cancelled = true;
    };
  }, [mediaID, applyInfo, loadAll]);

  const locked = (field: string): boolean =>
    info?.locks.some((lock) => lock.field === field && lock.locked) ?? false;

  const setLocks = (locks: FieldLockInfo[]) => {
    setInfo((prev) => (prev ? { ...prev, locks } : prev));
  };

  // refreshLocks re-reads only the lock state, so an artwork or plugin-field
  // change never discards unsaved edits in the details form.
  const refreshLocks = async () => {
    setLocks((await fetchMediaMetadata(mediaID)).locks);
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
    begin();
    try {
      setLocks(await setMediaFieldLock(mediaID, field, !locked(field)));
    } catch {
      setError(t("itemMetadataLockFailed"));
    }
  };

  const onRevert = async (field: string) => {
    begin();
    setBusy(true);
    try {
      setLocks(await setMediaFieldLock(mediaID, field, false, true));
      if (info?.refreshable) {
        applyInfo(await refreshMediaMetadata(mediaID));
        await loadAll(mediaID);
        setStatus(t("itemMetadataReverted"));
      } else {
        setStatus(t("itemMetadataRevertedUnlocked"));
      }
      changed();
    } catch {
      setError(t("itemMetadataRevertFailed"));
    } finally {
      setBusy(false);
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
    setBusy(true);
    try {
      applyInfo(await editMediaMetadata(mediaID, edit));
      setStatus(t("itemMetadataSaved"));
      onChanged?.();
    } catch {
      setError(t("itemMetadataSaveFailed"));
    } finally {
      setBusy(false);
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
    setBusy(true);
    try {
      await bulkEditApply([mediaID], edits);
      await refreshLocks();
      await loadFields(mediaID);
      setStatus(t("itemMetadataSaved"));
      onChanged?.();
    } catch {
      setError(t("itemMetadataSaveFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onChooseArtwork = async (slot: string, candidate: ArtworkCandidate) => {
    begin();
    try {
      // The server locks the slot as part of the pin.
      await pinArtworkCandidate(mediaID, slot, candidate.id);
      await refreshLocks();
      await loadSlot(mediaID, slot);
      changed();
    } catch {
      setError(t("couldNotPinArtworkCandidate"));
    }
  };

  const onUploadArtwork = async (slot: string, file: File | undefined) => {
    if (!file) return;
    begin();
    setBusy(true);
    try {
      await uploadMediaArtwork(mediaID, slot, file);
      await refreshLocks();
      await loadSlot(mediaID, slot);
      setStatus(t("itemMetadataArtworkUploaded"));
      changed();
    } catch {
      setError(t("itemMetadataArtworkUploadFailed"));
    } finally {
      setBusy(false);
      const input = fileInputs.current[slot];
      if (input) input.value = "";
    }
  };

  const onRefresh = async () => {
    begin();
    setBusy(true);
    try {
      applyInfo(await refreshMediaMetadata(mediaID));
      await loadAll(mediaID);
      setStatus(t("itemMetadataRefreshed"));
      changed();
    } catch {
      setError(t("itemMetadataRefreshFailed"));
    } finally {
      setBusy(false);
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
  if (info === undefined) return null;

  return (
    <div className="space-y-6">
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
        <Button type="button" onClick={onSaveDetails} disabled={busy}>
          {t("itemMetadataSave")}
        </Button>
      </section>

      <section className="space-y-3" aria-labelledby="item-metadata-fields">
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

      <section className="space-y-3" aria-labelledby="item-metadata-artwork">
        <h3 id="item-metadata-artwork" className="font-medium">
          {t("itemMetadataArtworkHeading")}
        </h3>
        <p className="text-muted-foreground text-sm">{t("itemMetadataArtworkLead")}</p>
        {SLOTS.map(({ slot, lock, key }) => {
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
                          className="h-24 w-auto rounded-md border"
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
                          onClick={() => onChooseArtwork(slot, candidate)}
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

      <section className="space-y-3" aria-labelledby="item-metadata-refresh">
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

      {status ? (
        <p role="status" className="text-sm">
          {status}
        </p>
      ) : null}
      <ErrorLine message={error} />
    </div>
  );
}
