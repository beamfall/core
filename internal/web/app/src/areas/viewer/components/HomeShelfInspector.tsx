import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import type {
  HomeArtworkSlot,
  HomeFilterSchemaInfo,
  HomeRandomSeedMode,
  HomeShelf,
  HomeShelfPreviewInfo,
  HomeShelfSourceType,
  HomeShelfStyle,
} from "@/lib/graphql/types";
import { fetchHomeFilterSchema, previewHomeShelf } from "@/lib/graphql/viewer";
import { useT } from "@/lib/i18n";
import { HomeRuleBuilder } from "./HomeRuleBuilder";

interface HomeShelfInspectorProps {
  shelf: HomeShelf;
  schema: HomeFilterSchemaInfo;
  onApply: (updated: HomeShelf) => void;
  onCancel: () => void;
}

const BUILTIN_OPTIONS = [
  { value: "continue-watching", label: "Continue Watching" },
  { value: "next-up", label: "Up Next" },
  { value: "continue-listening", label: "Continue Listening" },
  { value: "watchlist", label: "Watchlist" },
  { value: "recently-added", label: "Recently Added" },
  { value: "most-played", label: "Your Most-Played" },
  { value: "favorites", label: "Favorites" },
  { value: "random", label: "Random Discovery" },
];

const SORT_OPTIONS = [
  { value: "added", label: "Date Added" },
  { value: "title", label: "Title" },
  { value: "year", label: "Year" },
  { value: "rating", label: "My Rating" },
  { value: "playCount", label: "Play Count" },
  { value: "lastPlayed", label: "Last Played" },
  { value: "duration", label: "Duration" },
  { value: "random", label: "Random" },
];

export function HomeShelfInspector({
  shelf,
  schema: initialSchema,
  onApply,
  onCancel,
}: HomeShelfInspectorProps) {
  const t = useT();
  const [draft, setDraft] = useState<HomeShelf>(() => structuredClone(shelf));
  const [schema, setSchema] = useState<HomeFilterSchemaInfo>(initialSchema);
  const [preview, setPreview] = useState<HomeShelfPreviewInfo | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  const selectedLibsKey = (draft.source.libraryIDs ?? []).join(",");

  useEffect(() => {
    const libs = selectedLibsKey ? selectedLibsKey.split(",") : undefined;
    const controller = new AbortController();
    fetchHomeFilterSchema(libs, controller.signal)
      .then((res) => {
        if (!controller.signal.aborted) setSchema(res);
      })
      .catch(() => {
        // Keep current schema if refresh fails.
      });
    return () => controller.abort();
  }, [selectedLibsKey]);

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setPreviewLoading(true);
      previewHomeShelf(draft, 8, controller.signal)
        .then((res) => {
          if (!controller.signal.aborted) {
            setPreview(res);
            setPreviewLoading(false);
          }
        })
        .catch(() => {
          if (!controller.signal.aborted) {
            setPreviewLoading(false);
          }
        });
    }, 300);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [draft]);

  const selectedLibs = draft.source.libraryIDs ?? [];

  function switchSourceType(nextType: HomeShelfSourceType) {
    const libraryIDs = draft.source.libraryIDs;
    switch (nextType) {
      case "builtin":
        setDraft({
          ...draft,
          source: { type: "builtin", builtinKind: "recently-added", libraryIDs },
        });
        break;
      case "rules":
        setDraft({
          ...draft,
          source: {
            type: "rules",
            libraryIDs,
            rules: draft.source.rules ?? { match: "all", conditions: [] },
            sort: draft.source.sort ?? { by: "added", desc: true },
          },
        });
        break;
      case "collection":
      case "playlist": {
        const firstCol =
          schema.pinnables.smartCollections[0]?.id ?? schema.pinnables.collections[0]?.id ?? "";
        setDraft({
          ...draft,
          source: { type: "collection", collectionID: firstCol, libraryIDs },
        });
        break;
      }
      case "saved-filter":
        setDraft({
          ...draft,
          source: {
            type: "saved-filter",
            savedFilterID: schema.pinnables.savedFilters[0]?.id ?? "",
            libraryIDs,
          },
        });
        break;
      case "library":
        setDraft({
          ...draft,
          source: {
            type: "library",
            libraryIDs:
              libraryIDs && libraryIDs.length > 0
                ? libraryIDs
                : schema.libraries[0]
                  ? [schema.libraries[0].id]
                  : [],
          },
        });
        break;
      case "entity": {
        const firstEntity = schema.fields.find((f) => f.valueType === "entity");
        const kind = firstEntity?.key.replace(/^entity:/, "") ?? "";
        setDraft({
          ...draft,
          source: {
            type: "entity",
            entityKind: kind,
            entityName: firstEntity?.suggestions?.[0] ?? "",
            libraryIDs,
          },
        });
        break;
      }
    }
  }

  function toggleLibrary(id: string) {
    const next = selectedLibs.includes(id)
      ? selectedLibs.filter((x) => x !== id)
      : [...selectedLibs, id];
    setDraft({
      ...draft,
      source: { ...draft.source, libraryIDs: next },
    });
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={draft.title || "Edit shelf"}
      className="fixed inset-0 z-50 flex justify-end bg-background/80 backdrop-blur-xs"
    >
      <div className="flex h-full w-full max-w-2xl flex-col gap-4 overflow-y-auto border-l border-[var(--border)] bg-background p-6 shadow-2xl">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{draft.title || "Edit shelf"}</h3>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={onCancel}>
              {t("homeEditCancel")}
            </Button>
            <Button type="button" size="sm" onClick={() => onApply(draft)}>
              {t("homeShelfDone")}
            </Button>
          </div>
        </div>

        {/* Title & Source Type */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium">{t("homeShelfTitle")}</span>
            <input
              type="text"
              value={draft.title}
              maxLength={120}
              onChange={(e) => setDraft({ ...draft, title: e.target.value })}
              className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
            />
          </label>

          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium">Source type</span>
            <select
              aria-label="Source type"
              value={draft.source.type === "playlist" ? "collection" : draft.source.type}
              onChange={(e) => switchSourceType(e.target.value as HomeShelfSourceType)}
              className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
            >
              <option value="builtin">Built-in feed</option>
              <option value="rules">Smart rules</option>
              <option value="collection">Collection / Playlist</option>
              <option value="saved-filter">Saved filter</option>
              <option value="library">Library</option>
              <option value="entity">Tag / Category</option>
            </select>
          </label>
        </div>

        {/* Library Scope */}
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium">{t("homeShelfLibraries")}</span>
            <button
              type="button"
              onClick={() => setDraft({ ...draft, source: { ...draft.source, libraryIDs: [] } })}
              className={
                "text-xs underline-offset-2 " +
                (selectedLibs.length === 0
                  ? "font-semibold text-primary"
                  : "text-[var(--text-secondary)] hover:underline")
              }
            >
              {t("homeShelfAllLibraries")}
            </button>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {schema.libraries.map((lib) => {
              const active = selectedLibs.includes(lib.id);
              return (
                <button
                  key={lib.id}
                  type="button"
                  onClick={() => toggleLibrary(lib.id)}
                  className={
                    "rounded-full border px-3 py-1 text-xs font-medium transition-colors " +
                    (active
                      ? "border-primary bg-primary/15 text-primary"
                      : "border-[var(--border)] text-[var(--text-secondary)] hover:text-foreground")
                  }
                >
                  {lib.title}
                </button>
              );
            })}
          </div>
        </div>

        {/* Source-specific controls */}
        {draft.source.type === "builtin" && (
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium">Feed</span>
            <select
              value={draft.source.builtinKind ?? "recently-added"}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  source: { ...draft.source, builtinKind: e.target.value },
                })
              }
              className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
            >
              {BUILTIN_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </label>
        )}

        {(draft.source.type === "collection" || draft.source.type === "playlist") && (
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium">Collection / Playlist</span>
            <select
              value={draft.source.collectionID ?? draft.source.playlistID ?? ""}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  source: {
                    ...draft.source,
                    type: "collection",
                    collectionID: e.target.value,
                    playlistID: undefined,
                  },
                })
              }
              className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
            >
              {schema.pinnables.smartCollections.map((sc) => (
                <option key={sc.id} value={sc.id}>
                  Smart: {sc.name}
                </option>
              ))}
              {schema.pinnables.collections.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
        )}

        {draft.source.type === "saved-filter" && (
          <label className="flex flex-col gap-1.5 text-sm">
            <span className="font-medium">Saved Filter</span>
            <select
              value={draft.source.savedFilterID ?? ""}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  source: { ...draft.source, savedFilterID: e.target.value },
                })
              }
              className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
            >
              {schema.pinnables.savedFilters.map((sf) => (
                <option key={sf.id} value={sf.id}>
                  {sf.name}
                </option>
              ))}
            </select>
          </label>
        )}

        {draft.source.type === "entity" && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1.5 text-sm">
              <span className="font-medium">Category / Tag kind</span>
              <select
                value={draft.source.entityKind ?? ""}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    source: { ...draft.source, entityKind: e.target.value },
                  })
                }
                className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
              >
                {schema.fields
                  .filter((f) => f.valueType === "entity")
                  .map((f) => {
                    const kind = f.key.replace(/^entity:/, "");
                    return (
                      <option key={kind} value={kind}>
                        {typeof f.label === "string" ? f.label : (f.label.en ?? kind)}
                      </option>
                    );
                  })}
              </select>
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span className="font-medium">Name</span>
              <input
                type="text"
                list="home-entity-suggestions"
                value={draft.source.entityName ?? ""}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    source: { ...draft.source, entityName: e.target.value, entityID: "" },
                  })
                }
                className="h-9 rounded-md border border-[var(--border)] bg-background px-3 text-sm"
              />
              <datalist id="home-entity-suggestions">
                {(
                  schema.fields.find((f) => f.key === `entity:${draft.source.entityKind ?? ""}`)
                    ?.suggestions ?? []
                ).map((s) => (
                  <option key={s} value={s} />
                ))}
              </datalist>
            </label>
          </div>
        )}

        {(draft.source.type === "rules" || draft.source.type === "builtin") && (
          <div className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">
              {draft.source.type === "builtin" ? "Optional post-filter rules" : t("homeShelfRules")}
            </span>
            <HomeRuleBuilder
              group={draft.source.rules ?? { match: "all", conditions: [] }}
              fields={schema.fields}
              onChange={(rules) => setDraft({ ...draft, source: { ...draft.source, rules } })}
            />
          </div>
        )}

        {draft.source.type === "rules" && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <label className="flex flex-col gap-1 text-xs">
              <span className="font-medium">{t("homeShelfSortBy")}</span>
              <select
                value={draft.source.sort?.by ?? "added"}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    source: {
                      ...draft.source,
                      sort: {
                        by: e.target.value,
                        desc: draft.source.sort?.desc ?? true,
                        randomSeedMode: draft.source.sort?.randomSeedMode ?? "daily",
                      },
                    },
                  })
                }
                className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
              >
                {SORT_OPTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </label>

            <label className="flex items-center gap-2 self-end pb-1.5 text-xs">
              <Switch
                checked={draft.source.sort?.desc ?? true}
                onCheckedChange={(desc) =>
                  setDraft({
                    ...draft,
                    source: {
                      ...draft.source,
                      sort: {
                        by: draft.source.sort?.by ?? "added",
                        desc,
                        randomSeedMode: draft.source.sort?.randomSeedMode ?? "",
                      },
                    },
                  })
                }
              />
              <span>{t("homeShelfSortDesc")}</span>
            </label>

            {draft.source.sort?.by === "random" && (
              <label className="flex flex-col gap-1 text-xs">
                <span className="font-medium">Refresh cadence</span>
                <select
                  value={draft.source.sort?.randomSeedMode ?? "daily"}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      source: {
                        ...draft.source,
                        sort: {
                          by: "random",
                          desc: false,
                          randomSeedMode: e.target.value as HomeRandomSeedMode,
                        },
                      },
                    })
                  }
                  className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
                >
                  <option value="session">Every visit</option>
                  <option value="hourly">Hourly</option>
                  <option value="daily">Daily</option>
                </select>
              </label>
            )}
          </div>
        )}

        {/* Display Controls */}
        <div className="grid grid-cols-2 gap-3 rounded-md border border-[var(--border)] p-3 sm:grid-cols-4">
          <label className="flex flex-col gap-1 text-xs">
            <span className="font-medium">{t("homeShelfStyle")}</span>
            <select
              value={draft.display.style}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  display: { ...draft.display, style: e.target.value as HomeShelfStyle },
                })
              }
              className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
            >
              <option value="shelf">{t("homeShelfStyleRail")}</option>
              <option value="grid">{t("homeShelfStyleGrid")}</option>
              <option value="list">{t("homeShelfStyleList")}</option>
            </select>
          </label>

          <label className="flex flex-col gap-1 text-xs">
            <span className="font-medium">{t("homeShelfArtwork")}</span>
            <select
              value={draft.display.artworkSlot}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  display: {
                    ...draft.display,
                    artworkSlot: e.target.value as HomeArtworkSlot,
                  },
                })
              }
              className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
            >
              <option value="auto">Auto</option>
              <option value="poster">Poster</option>
              <option value="landscape">Landscape</option>
              <option value="square">Square</option>
            </select>
          </label>

          <label className="flex flex-col gap-1 text-xs">
            <span className="font-medium">
              {t("homeShelfLimit")} ({draft.display.limit})
            </span>
            <input
              type="range"
              min={4}
              max={50}
              value={draft.display.limit}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  display: { ...draft.display, limit: Number(e.target.value) },
                })
              }
              className="mt-2"
            />
          </label>

          <label className="flex items-center gap-2 self-end pb-1 text-xs">
            <Switch
              checked={draft.hideWhenEmpty}
              onCheckedChange={(hideWhenEmpty) => setDraft({ ...draft, hideWhenEmpty })}
            />
            <span>{t("homeShelfHideEmpty")}</span>
          </label>
        </div>

        {/* Live Preview Strip */}
        <div className="mt-auto flex flex-col gap-2 rounded-md border border-[var(--border)] bg-muted/20 p-3">
          <div className="flex items-center justify-between text-xs">
            <span className="font-medium">Live Preview</span>
            <span className="text-[var(--text-secondary)]">
              {previewLoading
                ? t("loading")
                : preview
                  ? preview.totalCount === 0
                    ? t("homeShelfPreviewEmpty")
                    : t("homeShelfPreviewMatches", { count: String(preview.totalCount) })
                  : ""}
            </span>
          </div>
          {preview && preview.items.length > 0 && (
            <div className="flex gap-2 overflow-x-auto pb-1">
              {preview.items.map((item) => (
                <div
                  key={item.id}
                  className="flex w-24 shrink-0 flex-col gap-1 rounded border border-[var(--border)]/60 bg-background/60 p-1.5"
                >
                  <div className="aspect-[2/3] w-full overflow-hidden rounded bg-muted/40">
                    {item.artwork?.poster ? (
                      <img
                        src={item.artwork.poster}
                        alt={item.title}
                        className="h-full w-full object-cover"
                      />
                    ) : (
                      <div className="flex h-full w-full items-center justify-center px-1 text-center text-[10px] text-[var(--text-secondary)]">
                        {item.title}
                      </div>
                    )}
                  </div>
                  <span className="truncate text-[11px] font-medium">{item.title}</span>
                  {item.summary && (
                    <span className="truncate text-[10px] text-[var(--text-secondary)]">
                      {item.summary}
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
