import { ArrowRight, Play, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { artworkBucketURL, artworkSlotURL } from "@/areas/viewer/lib/artwork";
import { mediaPath } from "@/areas/viewer/lib/mediaAddress";
import { Dialog as DialogPrimitive } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { SourceItem } from "@/lib/graphql/types";
import { fetchSourceItems } from "@/lib/graphql/viewer";
import { useT } from "@/lib/i18n";
import {
  type ExperienceMode,
  experienceModeLabel,
  itemMatchesLens,
  useExperienceLens,
  withExperienceModeParam,
} from "./experienceLens";
import { modeIconFor } from "./ModeLensPill";

interface ModePreviewState {
  loading: boolean;
  items: SourceItem[];
  sourceLabel: "continue-watching" | "continue-listening" | "recent";
}

const PREVIEW_LIMIT = 4;

async function fetchPreviewForMode(
  mode: ExperienceMode,
  signal: AbortSignal,
): Promise<ModePreviewState> {
  const seen = new Set<string>();
  const merged: SourceItem[] = [];
  const pushUnique = (items: SourceItem[]) => {
    for (const item of items) {
      if (item.locked || seen.has(item.id)) continue;
      if (!itemMatchesLens(item, mode)) continue;
      seen.add(item.id);
      merged.push(item);
      if (merged.length >= PREVIEW_LIMIT) break;
    }
  };

  const baseVars = {
    entityKind: "",
    libraryID: "",
    entityID: "",
    libraryIDs: mode.libraryIDs,
    fieldFilters: null,
    entityFilter: null,
    limit: PREVIEW_LIMIT,
    offset: 0,
    home: true,
  };

  if (mode.id === "watch") {
    const [cw, nextUp] = await Promise.all([
      fetchSourceItems({ ...baseVars, kind: "continue-watching" }, signal).catch(() => []),
      fetchSourceItems({ ...baseVars, kind: "next-up", value: "video" }, signal).catch(() => []),
    ]);
    pushUnique(cw);
    if (merged.length < PREVIEW_LIMIT) pushUnique(nextUp);
    if (merged.length > 0) {
      return { loading: false, items: merged, sourceLabel: "continue-watching" };
    }
    const recent = await fetchSourceItems({ ...baseVars, kind: "recently-added" }, signal).catch(
      () => [],
    );
    pushUnique(recent);
    return { loading: false, items: merged, sourceLabel: "recent" };
  }

  if (mode.id === "listen") {
    const [cw, nextUp] = await Promise.all([
      fetchSourceItems({ ...baseVars, kind: "continue-watching" }, signal).catch(() => []),
      fetchSourceItems({ ...baseVars, kind: "next-up", value: "audio" }, signal).catch(() => []),
    ]);
    pushUnique(cw);
    if (merged.length < PREVIEW_LIMIT) pushUnique(nextUp);
    if (merged.length > 0) {
      return { loading: false, items: merged, sourceLabel: "continue-listening" };
    }
    const [played, recent] = await Promise.all([
      fetchSourceItems({ ...baseVars, kind: "recently-played" }, signal).catch(() => []),
      fetchSourceItems({ ...baseVars, kind: "recently-added" }, signal).catch(() => []),
    ]);
    pushUnique(played);
    if (merged.length < PREVIEW_LIMIT) pushUnique(recent);
    return {
      loading: false,
      items: merged,
      sourceLabel: played.length > 0 ? "continue-listening" : "recent",
    };
  }

  const recent = await fetchSourceItems({ ...baseVars, kind: "recently-added" }, signal).catch(
    () => [],
  );
  pushUnique(recent);
  return { loading: false, items: merged, sourceLabel: "recent" };
}

function previewArtworkURL(item: SourceItem): string | undefined {
  const raw =
    artworkSlotURL(item, "poster") || artworkSlotURL(item, "thumb") || item.artwork?.poster || "";
  return raw ? artworkBucketURL(raw, "w320") : undefined;
}

function previewProgressPercent(item: SourceItem): number | null {
  const pos = item.engagement?.positionSeconds ?? 0;
  const dur = item.engagement?.durationSeconds ?? 0;
  if (pos <= 0 || dur <= 0) return null;
  return Math.max(4, Math.min(100, Math.round((pos / dur) * 100)));
}

export function LaunchLensOverlay() {
  const { modes, launchOpen, showModePill } = useExperienceLens();
  if (!launchOpen || !showModePill || !modes.some((m) => m.id !== "all")) return null;
  return <LaunchLensDialog />;
}

function LaunchLensDialog() {
  const t = useT();
  const { modes, launchOpen, setMode, dismissLaunchLens } = useExperienceLens();
  const [previews, setPreviews] = useState<Record<string, ModePreviewState>>({});

  const modalityModes = useMemo(() => modes.filter((m) => m.id !== "all"), [modes]);

  useEffect(() => {
    if (!launchOpen || !modalityModes.length) return;
    let alive = true;
    const controller = new AbortController();
    const initial: Record<string, ModePreviewState> = {};
    for (const m of modalityModes) {
      initial[m.id] = {
        loading: true,
        items: [],
        sourceLabel:
          m.id === "listen"
            ? "continue-listening"
            : m.id === "watch"
              ? "continue-watching"
              : "recent",
      };
    }
    setPreviews(initial);

    for (const m of modalityModes) {
      void fetchPreviewForMode(m, controller.signal).then((result) => {
        if (!alive || controller.signal.aborted) return;
        setPreviews((prev) => ({ ...prev, [m.id]: result }));
      });
    }
    return () => {
      alive = false;
      controller.abort();
    };
  }, [launchOpen, modalityModes]);

  return (
    <DialogPrimitive.Root
      open={launchOpen}
      onOpenChange={(open) => {
        if (!open) dismissLaunchLens();
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop className="bf-launch-backdrop" />
        <DialogPrimitive.Popup aria-labelledby="bf-launch-title" className="bf-launch-popup">
          <div className="bf-launch-header">
            <div className="min-w-0 flex-1">
              <h2 id="bf-launch-title" className="bf-launch-title">
                {t("lensOverlayTitle")}
              </h2>
              <p className="bf-launch-lead">{t("lensOverlayLead")}</p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setMode("all")}>
                {t("lensOverlayBrowseAll")}
              </Button>
              <button
                type="button"
                className="bf-icon-button"
                aria-label={t("lensOverlayClose")}
                onClick={dismissLaunchLens}
              >
                <X aria-hidden className="size-4" />
              </button>
            </div>
          </div>

          <div className="bf-launch-grid">
            {modalityModes.map((entry) => {
              const label = experienceModeLabel(entry, t);
              const Icon = modeIconFor(entry.id);
              const preview = previews[entry.id];
              const libCount = entry.libraryIDs.length;
              const libSummary =
                libCount === 1
                  ? t("lensOverlayLibrarySingle")
                  : t("lensOverlayLibraryPlural", { count: libCount });
              const sectionHeading =
                preview?.sourceLabel === "continue-watching"
                  ? t("lensOverlayContinueWatching")
                  : preview?.sourceLabel === "continue-listening"
                    ? t("lensOverlayContinueListening")
                    : t("lensOverlayRecent");

              return (
                <section key={entry.id} aria-label={label} className="bf-launch-card">
                  <button
                    type="button"
                    className="bf-launch-card-trigger"
                    aria-label={t("lensOverlayEnterMode", { mode: label })}
                    onClick={() => setMode(entry.id)}
                  >
                    <span className="bf-launch-badge" aria-hidden>
                      <Icon className="size-4" />
                    </span>
                    <span className="min-w-0 flex-1 text-left">
                      <span className="bf-launch-card-name">{label}</span>
                      <span className="bf-launch-card-meta">{libSummary}</span>
                    </span>
                    <ArrowRight aria-hidden className="bf-launch-arrow size-4 shrink-0" />
                  </button>

                  <div className="bf-launch-preview-area">
                    <div className="bf-launch-preview-label">{sectionHeading}</div>
                    {preview?.loading ? (
                      <div className="bf-launch-preview-row" aria-hidden>
                        {Array.from({ length: 3 }).map((_, idx) => (
                          <Skeleton
                            key={idx}
                            className="aspect-[16/10] w-full rounded-[var(--radius-field)]"
                          />
                        ))}
                      </div>
                    ) : preview && preview.items.length > 0 ? (
                      <div className="bf-launch-preview-row">
                        {preview.items.map((item) => {
                          const art = previewArtworkURL(item);
                          const pct = previewProgressPercent(item);
                          return (
                            <Link
                              key={item.id}
                              to={withExperienceModeParam(
                                mediaPath("media", item.id, { words: item.title }),
                                entry.id,
                              )}
                              className="bf-launch-thumb"
                              onClick={() => {
                                setMode(entry.id, { syncUrl: false, navigateHomeIfOutside: false });
                              }}
                            >
                              <div className="bf-launch-thumb-frame">
                                {art ? (
                                  <img
                                    src={art}
                                    alt=""
                                    className="h-full w-full object-cover"
                                    loading="lazy"
                                  />
                                ) : (
                                  <span className="bf-launch-thumb-fallback" aria-hidden>
                                    <Play className="size-4" />
                                  </span>
                                )}
                                {pct !== null && (
                                  <span className="bf-launch-thumb-progress" aria-hidden>
                                    <span style={{ width: `${pct}%` }} />
                                  </span>
                                )}
                              </div>
                              <span className="bf-launch-thumb-title">{item.title}</span>
                            </Link>
                          );
                        })}
                      </div>
                    ) : (
                      <button
                        type="button"
                        className="bf-launch-empty-btn"
                        onClick={() => setMode(entry.id)}
                      >
                        {t("lensOverlayEmptyPreview")}
                      </button>
                    )}
                  </div>
                </section>
              );
            })}
          </div>
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
