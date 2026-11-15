import { ListVideo, Plus } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useId, useState } from "react";
import { Link, useNavigate } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { createCollection } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { ViewerActions } from "../components/ViewerActions";
import { fetchViewerCollections, type LibraryCollections } from "../lib/collections";

// The viewer's Collections (/collections, CORE-1684): every ordered Collection
// the session can see, grouped by Library, each opening its own page. A Manager
// Device can start a new one here; Collections are household-shared, so there
// is no per-Profile owner to record.
export function CollectionListPage() {
  const t = useT();
  const navigate = useNavigate();
  const { manifest, isManager } = useSession();
  const libraries = manifest?.libraries ?? [];
  const [groups, setGroups] = useState<LibraryCollections[] | null>(null);
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      setGroups(await fetchViewerCollections(libraries));
    } catch {
      setFailed(true);
    }
    // The manifest's Library list is the input; its identity changes per read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [libraries.map((library) => library.id).join("\n")]);

  useEffect(() => {
    void load();
  }, [load]);

  const total = groups?.reduce((sum, group) => sum + group.collections.length, 0) ?? 0;
  const showLibrary = libraries.length > 1;

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="mydata mx-auto flex max-w-4xl flex-col gap-6">
        <header className="mydata-hero flex flex-col gap-1.5">
          <h1 className="bf-page-title">{t("viewerCollectionsTitle")}</h1>
          <p className="bf-lead max-w-2xl">{t("viewerCollectionsLead")}</p>
        </header>
        {isManager && libraries.length > 0 && (
          <NewCollectionForm
            libraries={libraries}
            onCreated={(libraryID, id) =>
              navigate(`/collections/${encodeURIComponent(libraryID)}/${encodeURIComponent(id)}`)
            }
          />
        )}
        {groups === null && !failed && (
          <p className="mydata-muted" role="status">
            {t("loading")}
          </p>
        )}
        {failed && (
          <div role="alert" className="bf-empty flex flex-col gap-3">
            <p className="text-sm text-[var(--text-secondary)]">
              {t("viewerCollectionsLoadFailed")}
            </p>
            <Button variant="outline" className="self-start" onClick={() => void load()}>
              {t("jobRetryAction")}
            </Button>
          </div>
        )}
        {groups !== null && total === 0 && (
          <p className="mydata-empty" role="status">
            {t("viewerCollectionsEmpty")}
          </p>
        )}
        {groups
          ?.filter((group) => group.collections.length > 0)
          .map((group) => (
            <section
              key={group.library.id}
              className="mydata-panel"
              aria-label={showLibrary ? group.library.title : t("viewerCollectionsTitle")}
            >
              {showLibrary && <h2 className="mydata-panel-title">{group.library.title}</h2>}
              <ul className="mydata-list">
                {group.collections.map((collection) => (
                  <li key={collection.id} className="mydata-row">
                    <ListVideo aria-hidden className="size-4 shrink-0" />
                    <Link
                      className="min-w-0 flex-1 truncate"
                      to={`/collections/${encodeURIComponent(group.library.id)}/${encodeURIComponent(collection.id)}`}
                    >
                      {collection.name}
                    </Link>
                    <span className="mydata-muted">
                      {t("viewerCollectionsItemCount", { count: collection.memberCount })}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          ))}
      </div>
    </AppShell>
  );
}

function NewCollectionForm({
  libraries,
  onCreated,
}: {
  libraries: readonly { id: string; title: string }[];
  onCreated: (libraryID: string, collectionID: string) => void;
}) {
  const t = useT();
  const nameID = useId();
  const libraryID = useId();
  const [name, setName] = useState("");
  const [library, setLibrary] = useState(libraries[0]?.id ?? "");
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const target = libraries.some((entry) => entry.id === library) ? library : libraries[0]?.id;

  async function submit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed || !target || busy) return;
    setBusy(true);
    setFailed(false);
    try {
      const created = await createCollection({ libraryID: target, name: trimmed });
      onCreated(target, created.id);
    } catch {
      setFailed(true);
      setBusy(false);
    }
  }

  return (
    <form className="mydata-panel" onSubmit={submit} aria-label={t("viewerCollectionsNew")}>
      <h2 className="mydata-panel-title">{t("viewerCollectionsNew")}</h2>
      <div className="flex flex-wrap items-end gap-3">
        <label htmlFor={nameID} className="flex min-w-48 flex-1 flex-col gap-1 text-sm">
          {t("viewerCollectionsName")}
          <Input
            id={nameID}
            value={name}
            maxLength={200}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        {libraries.length > 1 && (
          <label htmlFor={libraryID} className="flex flex-col gap-1 text-sm">
            {t("viewerCollectionsLibrary")}
            <NativeSelect
              id={libraryID}
              className="h-10 rounded-md border border-input bg-[var(--control-bg)] px-3 text-sm"
              value={target ?? ""}
              onValueChange={setLibrary}
            >
              {libraries.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.title}
                </option>
              ))}
            </NativeSelect>
          </label>
        )}
        <Button type="submit" disabled={busy || !name.trim()}>
          <Plus aria-hidden />
          {t("viewerCollectionsCreate")}
        </Button>
      </div>
      {failed && (
        <p role="alert" className="text-sm text-[var(--state-danger)]">
          {t("viewerCollectionsCreateFailed")}
        </p>
      )}
    </form>
  );
}
