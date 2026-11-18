import { type ReactNode, useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
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

// Per-item metadata editor (CORE-1715). Edits the core details through
// editMediaMetadata, plugin-declared fields through bulkEditApply and artwork
// through pinArtworkCandidate. Every edit is a manual-rank write, which is
// what a lock is: identification, refresh and rescans write below that rank
// and leave the field alone until it is unlocked here. Refresh re-runs the
// provider that identified the item; matching an unidentified item stays with
// Fix Match.
export function ItemMetadataPage() {
  const { t, displayLocale } = useI18n();
  const navigate = useNavigate();
  const params = useParams<{ mediaID?: string }>();
  const mediaID = params.mediaID ?? "";
  const [idInput, setIdInput] = useState(mediaID);
  // undefined = loading, null = unavailable.
  const [info, setInfo] = useState<MediaMetadataInfo | null | undefined>(undefined);
  const [draft, setDraft] = useState<Draft>({ title: "", sortTitle: "", summary: "", year: "" });
  const [fields, setFields] = useState<EntityFieldInfo[]>([]);
  const [fieldDraft, setFieldDraft] = useState<Record<string, string>>({});
  const [artwork, setArtwork] = useState<Record<string, ArtworkCandidate[] | null>>({});
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

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
    setIdInput(mediaID);
    setStatus("");
    setError("");
    setArtwork({});
    setFields([]);
    if (!mediaID) {
      setInfo(undefined);
      return;
    }
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

  const onOpen = () => {
    const id = idInput.trim();
    if (id) navigate(`/manage/items/${encodeURIComponent(id)}`);
  };

  const onToggleLock = async (field: string) => {
    setStatus("");
    setError("");
    try {
      setLocks(await setMediaFieldLock(mediaID, field, !locked(field)));
    } catch {
      setError(t("itemMetadataLockFailed"));
    }
  };

  const onSaveDetails = async () => {
    if (!info) return;
    setStatus("");
    setError("");
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
    } catch {
      setError(t("itemMetadataSaveFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onSaveFields = async () => {
    setStatus("");
    setError("");
    const changed = fields
      .filter((f) => (fieldDraft[f.name] ?? f.value) !== f.value)
      .map((f) => ({ name: f.name, type: f.type, value: fieldDraft[f.name] ?? "" }));
    if (changed.length === 0) {
      setStatus(t("itemMetadataNoChanges"));
      return;
    }
    setBusy(true);
    try {
      await bulkEditApply([mediaID], changed);
      applyInfo(await fetchMediaMetadata(mediaID));
      await loadFields(mediaID);
      setStatus(t("itemMetadataSaved"));
    } catch {
      setError(t("itemMetadataSaveFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onChooseArtwork = async (slot: string, lock: string, candidate: ArtworkCandidate) => {
    setStatus("");
    setError("");
    try {
      await pinArtworkCandidate(mediaID, slot, candidate.id);
      setLocks(await setMediaFieldLock(mediaID, lock, true));
      await loadSlot(mediaID, slot);
    } catch {
      setError(t("couldNotPinArtworkCandidate"));
    }
  };

  const onRefresh = async () => {
    setStatus("");
    setError("");
    setBusy(true);
    try {
      applyInfo(await refreshMediaMetadata(mediaID));
      await loadAll(mediaID);
      setStatus(t("itemMetadataRefreshed"));
    } catch {
      setError(t("itemMetadataRefreshFailed"));
    } finally {
      setBusy(false);
    }
  };

  const lockControl = (field: string, label: string) => {
    const isLocked = locked(field);
    return (
      <div className="flex items-center gap-2">
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
        <div className="flex items-center justify-between gap-3">
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

      <div className="mt-4 space-y-6">
        {!mediaID ? (
          <EmptyLine message={t("itemMetadataEmpty")} />
        ) : info === null ? (
          <ErrorLine message={t("itemMetadataUnavailable")} />
        ) : info === undefined ? null : (
          <>
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
                        <div className="flex items-center justify-between gap-3">
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
                return (
                  <div key={slot} className="space-y-2">
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-sm font-medium">{t(key)}</span>
                      {lockControl(lock, t(key))}
                    </div>
                    {!candidates || candidates.length === 0 ? (
                      <EmptyLine message={t("itemMetadataArtworkNone")} />
                    ) : (
                      <ul className="flex flex-wrap gap-3">
                        {candidates.map((candidate) => (
                          <li key={candidate.id} className="space-y-1">
                            {isRelativePreviewURL(candidate.previewURL) ? (
                              <img
                                src={candidate.previewURL}
                                alt=""
                                className="h-24 w-auto rounded-md border"
                              />
                            ) : null}
                            {candidate.applied ? (
                              <StatusBadge state="green">
                                {t("itemMetadataArtworkApplied")}
                              </StatusBadge>
                            ) : (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
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

            <section className="space-y-3" aria-labelledby="item-metadata-refresh">
              <h3 id="item-metadata-refresh" className="font-medium">
                {t("itemMetadataRefreshHeading")}
              </h3>
              <p className="text-muted-foreground text-sm">
                {info.refreshable
                  ? t("itemMetadataRefreshLead")
                  : t("itemMetadataRefreshNeedsMatch")}
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
          </>
        )}
        {status ? (
          <p role="status" className="text-sm">
            {status}
          </p>
        ) : null}
        <ErrorLine message={error} />
      </div>
    </ManageSection>
  );
}
