import { ArrowDown, ArrowUp, ChevronLeft, Pencil, Play, Plus, Search, Trash2 } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useId, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  addCollectionMedia,
  type CollectionInfo,
  removeCollectionMedia,
  reorderCollectionMedia,
  searchResult,
} from "@/lib/graphql";
import type { SearchResultItem, SourceItem } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession, useViewerIdentity } from "@/lib/session";
import { SlideshowButton } from "../../slideshow/SlideshowButton";
import { MediaItemCard } from "../components/MediaItemCard";
import { ViewerActions } from "../components/ViewerActions";
import { repeatedTitles, showsYear } from "../lib/cardTitles";
import { fetchCollection, fetchCollectionItems, memberOrder, moveEntry } from "../lib/collections";
import { mediaPath } from "../lib/mediaAddress";
import { navTitleFor, type PlayerNavState, playQueueOf, queueWindow } from "../lib/watchQueue";

// One ordered Collection's own page (/collections/:libraryId/:collectionId,
// CORE-1684): a playlist or a box set, its entries in curated order. Play all
// hands the whole order to the player as its queue, so previous/next and
// auto-advance walk the list. A Manager Device can reorder, remove and add
// entries; Collections are household-shared, so every Profile sees the change.
export function CollectionPage() {
  const t = useT();
  const navigate = useNavigate();
  const identity = useViewerIdentity();
  const { isManager } = useSession();
  const { libraryId = "", collectionId = "" } = useParams();
  const [collection, setCollection] = useState<CollectionInfo | null | undefined>(undefined);
  const [items, setItems] = useState<SourceItem[]>([]);
  const [failed, setFailed] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);
  const queue = useMemo(() => playQueueOf(items), [items]);
  const repeated = useMemo(() => repeatedTitles(items), [items]);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      const [found, entries] = await Promise.all([
        fetchCollection(libraryId, collectionId),
        fetchCollectionItems(collectionId),
      ]);
      setCollection(found);
      setItems(found ? entries : []);
    } catch {
      setFailed(true);
    }
  }, [libraryId, collectionId]);

  useEffect(() => {
    setCollection(undefined);
    setItems([]);
    void load();
  }, [load]);

  // Every edit answers with the Collection's new state; the entries are read
  // again through the feed so artwork, engagement and reveal stay server-owned.
  async function save(change: () => Promise<CollectionInfo>): Promise<boolean> {
    setSaving(true);
    setSaveFailed(false);
    try {
      const next = await change();
      setCollection(next);
      setItems(await fetchCollectionItems(next.id));
      return true;
    } catch {
      setSaveFailed(true);
      return false;
    } finally {
      setSaving(false);
    }
  }

  function move(id: string, delta: -1 | 1) {
    if (!collection) return;
    const order = moveEntry(
      memberOrder(collection),
      items.map((item) => item.id),
      id,
      delta,
    );
    if (!order) return;
    void save(() => reorderCollectionMedia(collection.id, order));
  }

  function remove(id: string) {
    if (!collection) return;
    void save(() => removeCollectionMedia(collection.id, [id]));
  }

  function playAll() {
    const first = queue[0];
    if (!first) return;
    const title = navTitleFor(identity, first.title);
    const state: PlayerNavState = identity
      ? { title, queue: queueWindow(queue, first.id), queueIdentity: identity }
      : { title };
    navigate(mediaPath("watch", first.id), { state });
  }

  const back = (
    <Button
      variant="ghost"
      size="sm"
      className="-ml-2 self-start"
      onClick={() => navigate("/collections")}
    >
      <ChevronLeft aria-hidden />
      {t("viewerCollectionsTitle")}
    </Button>
  );

  if (failed) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="flex flex-col gap-3">
          {back}
          <div role="alert" className="bf-empty flex flex-col gap-3">
            <p className="text-sm text-[var(--text-secondary)]">
              {t("viewerCollectionsLoadFailed")}
            </p>
            <Button variant="outline" className="self-start" onClick={() => void load()}>
              {t("jobRetryAction")}
            </Button>
          </div>
        </div>
      </AppShell>
    );
  }

  if (collection === null) {
    return (
      <AppShell actions={<ViewerActions />}>
        <div className="flex flex-col gap-3">
          {back}
          <div className="bf-empty" role="status">
            <p className="text-sm text-[var(--text-secondary)]">{t("viewerCollectionNotFound")}</p>
          </div>
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-6">
        <div className="flex flex-col items-start gap-1">
          {back}
          {collection === undefined ? (
            <Skeleton aria-hidden className="bf-shimmer h-8 w-64" />
          ) : (
            <>
              <h1 className="bf-page-title">{collection.name}</h1>
              <p className="mydata-muted">
                {t("viewerCollectionsItemCount", { count: items.length })}
              </p>
            </>
          )}
        </div>
        {collection && (
          <div className="flex flex-wrap gap-2">
            <Button onClick={playAll} disabled={queue.length === 0}>
              <Play aria-hidden />
              {t("viewerCollectionPlayAll")}
            </Button>
            {collectionId && (
              <SlideshowButton
                source={{ kind: "collection", collectionID: collectionId }}
                items={items}
              />
            )}
            {isManager && (
              <Button
                variant="outline"
                aria-pressed={editing}
                onClick={() => setEditing((value) => !value)}
              >
                <Pencil aria-hidden />
                {editing ? t("viewerCollectionDone") : t("viewerCollectionEdit")}
              </Button>
            )}
          </div>
        )}
        {collection === undefined && (
          <div aria-busy="true" className="bf-grid">
            <span className="sr-only">{t("loading")}</span>
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} aria-hidden className="bf-shimmer aspect-[2/3] w-full" />
            ))}
          </div>
        )}
        {saveFailed && (
          <p role="alert" className="text-sm text-[var(--state-danger)]">
            {t("viewerCollectionSaveFailed")}
          </p>
        )}
        {collection && items.length === 0 && (
          <p className="mydata-empty" role="status">
            {t("viewerCollectionEntriesEmpty")}
          </p>
        )}
        {collection && editing && isManager ? (
          <>
            <ol className="mydata-list" aria-label={collection.name}>
              {items.map((item, index) => (
                <li key={item.id} className="mydata-row">
                  <span className="mydata-muted w-6 text-right tabular-nums">{index + 1}</span>
                  <span className="min-w-0 flex-1 truncate" title={item.title}>
                    {item.title}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("viewerCollectionMoveUp", { title: item.title })}
                    disabled={saving || index === 0}
                    onClick={() => move(item.id, -1)}
                  >
                    <ArrowUp aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("viewerCollectionMoveDown", { title: item.title })}
                    disabled={saving || index === items.length - 1}
                    onClick={() => move(item.id, 1)}
                  >
                    <ArrowDown aria-hidden />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("viewerCollectionRemove", { title: item.title })}
                    disabled={saving}
                    onClick={() => remove(item.id)}
                  >
                    <Trash2 aria-hidden />
                  </Button>
                </li>
              ))}
            </ol>
            <AddEntries
              libraryID={libraryId}
              members={collection.members.map((member) => member.mediaID)}
              disabled={saving}
              onAdd={(id) => save(() => addCollectionMedia(collection.id, [id]))}
            />
          </>
        ) : (
          items.length > 0 && (
            <ul className="bf-grid">
              {items.map((item, index) => (
                <MediaItemCard
                  key={item.id}
                  item={item}
                  posInSet={index + 1}
                  setSize={items.length}
                  queue={queue}
                  showYear={showsYear(item, repeated)}
                />
              ))}
            </ul>
          )
        )}
      </div>
    </AppShell>
  );
}

// Adds entries from a keyword search of the Collection's own Library: the
// server keeps a Collection to one Library, so other Libraries' rows are not
// offered.
function AddEntries({
  libraryID,
  members,
  disabled,
  onAdd,
}: {
  libraryID: string;
  members: readonly string[];
  disabled: boolean;
  onAdd: (id: string) => Promise<boolean>;
}) {
  const t = useT();
  const searchID = useId();
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<SearchResultItem[] | null>(null);
  const [message, setMessage] = useState<{ text: string; failed: boolean } | null>(null);

  async function submit(event: FormEvent) {
    event.preventDefault();
    const query = term.trim();
    if (!query) return;
    setMessage(null);
    try {
      const response = await searchResult(query, "keyword");
      setResults(
        response.items.filter((item) => item.kind === "media" && item.libraryID === libraryID),
      );
    } catch {
      setResults([]);
    }
  }

  async function add(item: SearchResultItem) {
    setMessage(null);
    const added = await onAdd(item.id);
    setMessage({
      text: t(added ? "viewerCollectionAdded" : "viewerCollectionAddFailed", { title: item.title }),
      failed: !added,
    });
  }

  const offered = results?.filter((item) => !members.includes(item.id)) ?? null;

  return (
    <section className="mydata-panel" aria-labelledby={`${searchID}-heading`}>
      <h2 id={`${searchID}-heading`} className="mydata-panel-title">
        {t("viewerCollectionAddHeading")}
      </h2>
      <form className="flex gap-2" onSubmit={submit}>
        <Input
          id={searchID}
          type="search"
          data-unsaved-until-submit
          aria-label={t("viewerCollectionAddSearch")}
          placeholder={t("viewerCollectionAddSearch")}
          value={term}
          onChange={(event) => setTerm(event.target.value)}
        />
        <Button type="submit" variant="outline" aria-label={t("viewerCollectionAddSearch")}>
          <Search aria-hidden />
        </Button>
      </form>
      {offered !== null && offered.length === 0 && (
        <p className="mydata-empty" role="status">
          {t("viewerCollectionAddNoResults")}
        </p>
      )}
      {offered !== null && offered.length > 0 && (
        <ul className="mydata-list">
          {offered.map((item) => (
            <li key={item.id} className="mydata-row">
              <span className="min-w-0 flex-1 truncate" title={item.title}>
                {item.title}
              </span>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t("viewerCollectionAddAction", { title: item.title })}
                disabled={disabled}
                onClick={() => void add(item)}
              >
                <Plus aria-hidden />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {message && (
        <p
          role={message.failed ? "alert" : "status"}
          className={message.failed ? "text-sm text-[var(--state-danger)]" : "mydata-muted"}
        >
          {message.text}
        </p>
      )}
    </section>
  );
}
