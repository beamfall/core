import { Sparkles, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { HomeFilterSchemaInfo, HomeShelf } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";

interface HomeAddShelfDialogProps {
  schema: HomeFilterSchemaInfo;
  onAddShelves: (shelves: HomeShelf[], openInspectorForFirst?: boolean) => void;
  onClose: () => void;
}

function makeShelfID(): string {
  return "hs_" + Math.random().toString(16).slice(2, 14);
}

const BUILTIN_FEEDS = [
  { kind: "continue-watching", title: "Continue Watching" },
  { kind: "next-up", title: "Up Next" },
  { kind: "continue-listening", title: "Continue Listening" },
  { kind: "watchlist", title: "Watchlist" },
  { kind: "recently-added", title: "Recently Added" },
  { kind: "most-played", title: "Your Most-Played" },
  { kind: "favorites", title: "Favorites" },
];

export function HomeAddShelfDialog({ schema, onAddShelves, onClose }: HomeAddShelfDialogProps) {
  const t = useT();

  function addSplitRecentlyAdded() {
    const shelves: HomeShelf[] = schema.libraries.map((lib) => ({
      id: makeShelfID(),
      title: `Recently Added — ${lib.title}`,
      hidden: false,
      hideWhenEmpty: true,
      display: { style: "shelf", artworkSlot: "auto", limit: 20 },
      source: {
        type: "builtin",
        builtinKind: "recently-added",
        libraryIDs: [lib.id],
      },
    }));
    if (shelves.length > 0) onAddShelves(shelves);
  }

  function addUnwatchedGems() {
    onAddShelves([
      {
        id: makeShelfID(),
        title: "Unwatched & Highly Rated",
        hidden: false,
        hideWhenEmpty: true,
        display: { style: "shelf", artworkSlot: "auto", limit: 20 },
        source: {
          type: "rules",
          rules: {
            match: "all",
            conditions: [
              { field: "watched", op: "isFalse" },
              { field: "rating", op: "gte", value: 7 },
            ],
          },
          sort: { by: "rating", desc: true },
        },
      },
    ]);
  }

  function addRediscoverFavorites() {
    onAddShelves([
      {
        id: makeShelfID(),
        title: "Rediscover Favorites",
        hidden: false,
        hideWhenEmpty: true,
        display: { style: "shelf", artworkSlot: "auto", limit: 20 },
        source: {
          type: "rules",
          rules: {
            match: "all",
            conditions: [{ field: "favorited", op: "isTrue" }],
          },
          sort: { by: "random", desc: false, randomSeedMode: "daily" },
        },
      },
    ]);
  }

  function addDailyDiscovery() {
    onAddShelves([
      {
        id: makeShelfID(),
        title: "Daily Discovery",
        hidden: false,
        hideWhenEmpty: true,
        display: { style: "grid", artworkSlot: "auto", limit: 12 },
        source: {
          type: "rules",
          rules: {
            match: "all",
            conditions: [{ field: "watched", op: "isFalse" }],
          },
          sort: { by: "random", desc: false, randomSeedMode: "daily" },
        },
      },
    ]);
  }

  function addCustomSmartShelf() {
    onAddShelves(
      [
        {
          id: makeShelfID(),
          title: "Custom Smart Shelf",
          hidden: false,
          hideWhenEmpty: true,
          display: { style: "shelf", artworkSlot: "auto", limit: 20 },
          source: {
            type: "rules",
            rules: { match: "all", conditions: [] },
            sort: { by: "added", desc: true },
          },
        },
      ],
      true,
    );
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t("homeAddShelf")}
      className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 p-4 backdrop-blur-xs"
    >
      <div className="flex max-h-[85vh] w-full max-w-2xl flex-col gap-5 overflow-y-auto rounded-xl border border-[var(--border)] bg-background p-6 shadow-2xl">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold">{t("homeAddShelf")}</h3>
          <Button type="button" variant="ghost" size="icon-sm" aria-label="Close" onClick={onClose}>
            <X className="size-4" aria-hidden />
          </Button>
        </div>

        {/* Custom Smart Shelf CTA */}
        <button
          type="button"
          onClick={addCustomSmartShelf}
          className="flex items-center gap-3 rounded-lg border border-primary/40 bg-primary/10 p-4 text-left transition-colors hover:bg-primary/15"
        >
          <Sparkles className="size-5 shrink-0 text-primary" aria-hidden />
          <div>
            <div className="font-medium">{t("homeSmartShelfTitle")}</div>
            <div className="text-xs text-[var(--text-secondary)]">{t("homeSmartShelfDesc")}</div>
          </div>
        </button>

        {/* Quick Presets */}
        <div className="flex flex-col gap-2">
          <h4 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
            {t("homePresetsTitle")}
          </h4>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <Button
              type="button"
              variant="outline"
              className="justify-start"
              onClick={addSplitRecentlyAdded}
            >
              {t("homePresetSplitRecent")}
            </Button>
            <Button
              type="button"
              variant="outline"
              className="justify-start"
              onClick={addUnwatchedGems}
            >
              {t("homePresetUnwatchedGems")}
            </Button>
            <Button
              type="button"
              variant="outline"
              className="justify-start"
              onClick={addRediscoverFavorites}
            >
              {t("homePresetFavorites")}
            </Button>
            <Button
              type="button"
              variant="outline"
              className="justify-start"
              onClick={addDailyDiscovery}
            >
              {t("homePresetDailyRandom")}
            </Button>
          </div>
        </div>

        {/* Built-in Feeds */}
        <div className="flex flex-col gap-2">
          <h4 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
            {t("homeBuiltinTitle")}
          </h4>
          <div className="flex flex-wrap gap-2">
            {BUILTIN_FEEDS.map((feed) => (
              <Button
                key={feed.kind}
                type="button"
                variant="outline"
                size="sm"
                onClick={() =>
                  onAddShelves([
                    {
                      id: makeShelfID(),
                      title: feed.title,
                      hidden: false,
                      hideWhenEmpty: true,
                      display: { style: "shelf", artworkSlot: "auto", limit: 20 },
                      source: { type: "builtin", builtinKind: feed.kind },
                    },
                  ])
                }
              >
                {feed.title}
              </Button>
            ))}
          </div>
        </div>

        {/* Pin Existing */}
        {(schema.pinnables.collections.length > 0 ||
          schema.pinnables.smartCollections.length > 0 ||
          schema.pinnables.savedFilters.length > 0 ||
          schema.fields.some((f) => f.valueType === "entity") ||
          schema.libraries.length > 0) && (
          <div className="flex flex-col gap-2">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-[var(--text-secondary)]">
              {t("homePinExistingTitle")}
            </h4>
            <div className="flex flex-wrap gap-2">
              {schema.fields.some((f) => f.valueType === "entity") && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    const firstEntity = schema.fields.find((f) => f.valueType === "entity");
                    const kind = firstEntity?.key.replace(/^entity:/, "") ?? "";
                    onAddShelves(
                      [
                        {
                          id: makeShelfID(),
                          title: "Pinned Category",
                          hidden: false,
                          hideWhenEmpty: true,
                          display: { style: "shelf", artworkSlot: "auto", limit: 20 },
                          source: {
                            type: "entity",
                            entityKind: kind,
                            entityName: firstEntity?.suggestions?.[0] ?? "",
                          },
                        },
                      ],
                      true,
                    );
                  }}
                >
                  Pin by Tag / Category…
                </Button>
              )}
              {schema.pinnables.smartCollections.map((sc) => (
                <Button
                  key={sc.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    onAddShelves([
                      {
                        id: makeShelfID(),
                        title: sc.name,
                        hidden: false,
                        hideWhenEmpty: true,
                        display: { style: "shelf", artworkSlot: "auto", limit: 20 },
                        source: {
                          type: "collection",
                          collectionID: sc.id,
                          libraryIDs: [sc.libraryID],
                        },
                      },
                    ])
                  }
                >
                  Smart: {sc.name}
                </Button>
              ))}
              {schema.pinnables.collections.map((c) => (
                <Button
                  key={c.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    onAddShelves([
                      {
                        id: makeShelfID(),
                        title: c.name,
                        hidden: false,
                        hideWhenEmpty: true,
                        display: { style: "shelf", artworkSlot: "auto", limit: 20 },
                        source: {
                          type: "collection",
                          collectionID: c.id,
                          libraryIDs: [c.libraryID],
                        },
                      },
                    ])
                  }
                >
                  {c.name}
                </Button>
              ))}
              {schema.pinnables.savedFilters.map((sf) => (
                <Button
                  key={sf.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    onAddShelves([
                      {
                        id: makeShelfID(),
                        title: sf.name,
                        hidden: false,
                        hideWhenEmpty: true,
                        display: { style: "shelf", artworkSlot: "auto", limit: 20 },
                        source: {
                          type: "saved-filter",
                          savedFilterID: sf.id,
                          libraryIDs: [sf.libraryID],
                        },
                      },
                    ])
                  }
                >
                  Filter: {sf.name}
                </Button>
              ))}
              {schema.libraries.map((lib) => (
                <Button
                  key={lib.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    onAddShelves([
                      {
                        id: makeShelfID(),
                        title: lib.title,
                        hidden: false,
                        hideWhenEmpty: true,
                        display: { style: "shelf", artworkSlot: "auto", limit: 20 },
                        source: {
                          type: "library",
                          libraryIDs: [lib.id],
                        },
                      },
                    ])
                  }
                >
                  Library: {lib.title}
                </Button>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
