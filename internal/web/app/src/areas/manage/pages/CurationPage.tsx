import { ArrowDown, ArrowUp } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError } from "@/lib/api";
import { displayString } from "@/lib/display";
import {
  fetchEntity,
  fetchEntityRelations,
  fetchMedia,
  fetchMediaFiles,
  fetchMediaRelations,
  linkMediaRelation,
  searchResult,
  upsertEntityRelation,
} from "@/lib/graphql";
import type {
  EntityRelationDetail,
  MediaFileDetail,
  MediaRelationDetail,
  SearchResultItem,
} from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";

type SubjectType = "media" | "entity";

// Manage › Versions & links (CORE-2913; WEB-5B, ADR-0001/0005/0009/0061). One
// page for the structure around a catalog subject that no other Manage page
// edits: a Media item's backing files, its Extras and alternate Editions
// (mediaFiles / mediaRelations / linkMediaRelation), and the ordered items
// linked to an Entity (entityRelations / upsertEntityRelation). The subject is
// found by catalog search and kept in the URL (?open=<id>&as=media|entity) so a
// reload reopens it. Fields are edited on Item metadata, not here. Every write
// is manager-gated and reveal-filtered server-side; this surface is only an
// affordance. Server strings render as React text only (never HTML, ADR-0012
// §5). "kind" is a plugin display discriminant, never a schema type.
export function CurationPage() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const openID = params.get("open")?.trim() ?? "";
  const openType: SubjectType = params.get("as") === "entity" ? "entity" : "media";

  const open = (item: SearchResultItem) => {
    setParams({ open: item.id, as: isEntity(item) ? "entity" : "media" });
  };
  const close = () => setParams({});

  if (!openID) {
    return (
      <ManageSection title={t("curationFindHeading")} lead={t("curationFindLead")}>
        <ul className="mb-4 list-disc space-y-1 pl-5 text-sm text-[var(--text-muted)]">
          <li>{t("curationJobFiles")}</li>
          <li>{t("curationJobLinks")}</li>
          <li>{t("curationJobOrder")}</li>
        </ul>
        <ItemSearch
          id="curation-find"
          label={t("curationSearchLabel")}
          actionLabel={t("curationOpen")}
          onPick={open}
        />
      </ManageSection>
    );
  }
  return openType === "entity" ? (
    <EntityView key={openID} id={openID} onClose={close} />
  ) : (
    <MediaView key={openID} id={openID} onClose={close} />
  );
}

// --- Catalog search picker ---------------------------------------------------

// The wire discriminant: "entity" or "media". entityKind is display metadata.
function isEntity(item: SearchResultItem): boolean {
  return item.kind === "entity";
}

// Keyword catalog search (reveal-filtered server-side) with one action per
// result. `mediaOnly` hides Entity rows where only a Media item can be linked.
function ItemSearch({
  id,
  label,
  actionLabel,
  onPick,
  mediaOnly = false,
  excludeID,
  excludeIDs,
  disabled = false,
}: {
  id: string;
  label: string;
  actionLabel: string;
  onPick: (item: SearchResultItem) => void | Promise<void>;
  mediaOnly?: boolean;
  excludeID?: string;
  excludeIDs?: ReadonlySet<string>;
  disabled?: boolean;
}) {
  const { t, displayLocale } = useI18n();
  const [term, setTerm] = useState("");
  // The submitted query and how many raw results it has returned so far, so
  // "More results" asks for the next page of the same search.
  const [search, setSearch] = useState<{ query: string; fetched: number; hasMore: boolean }>({
    query: "",
    fetched: 0,
    hasMore: false,
  });
  const [results, setResults] = useState<SearchResultItem[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const request = useRef<AbortController | null>(null);

  useEffect(() => () => request.current?.abort(), []);

  const run = async (query: string, offset: number) => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setError("");
    setBusy(true);
    try {
      const response = await searchResult(query, "keyword", controller.signal, { offset });
      if (controller.signal.aborted) return;
      const page = response.items.filter(
        (item) =>
          item.id !== excludeID && !excludeIDs?.has(item.id) && !(mediaOnly && isEntity(item)),
      );
      setResults((prev) => (offset === 0 || !prev ? page : [...prev, ...page]));
      setSearch({
        query,
        fetched: offset + response.items.length,
        hasMore: Boolean(response.hasMore) && response.items.length > 0,
      });
    } catch {
      if (controller.signal.aborted) return;
      if (offset === 0) setResults(null);
      setError(t("curationSearchFailed"));
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const query = term.trim();
    if (query) void run(query, 0);
  };

  const kindLabel = (item: SearchResultItem) => {
    if (isEntity(item)) {
      return displayString(item.entityKindDisplayName, displayLocale) || t("curationKindEntity");
    }
    return t("curationKindMedia");
  };

  return (
    <div>
      <form className="flex flex-wrap items-end gap-3" onSubmit={onSubmit}>
        <div className="min-w-48 grow space-y-1">
          <Label htmlFor={id}>{label}</Label>
          <Input
            id={id}
            type="search"
            value={term}
            autoComplete="off"
            spellCheck={false}
            placeholder={t("curationSearchPlaceholder")}
            onChange={(e) => setTerm(e.target.value)}
          />
        </div>
        <Button type="submit" variant="outline" disabled={!term.trim() || busy}>
          {t("curationSearchSubmit")}
        </Button>
      </form>
      <ErrorLine message={error} />
      {results && results.length === 0 && !search.hasMore && (
        <div role="status">
          <EmptyLine message={t("curationSearchNoResults")} />
        </div>
      )}
      {results && results.length > 0 && (
        <ul className="mt-3 divide-y divide-[var(--line-soft)] rounded-md border border-[var(--line-soft)]">
          {results.map((item) => (
            <li key={item.id} className="flex items-center justify-between gap-3 px-3 py-2">
              <div className="min-w-0">
                <div className="break-words text-sm font-medium text-[var(--text-primary)]">
                  {item.title || item.id}
                </div>
                <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--text-muted)]">
                  <StatusBadge state={isEntity(item) ? "cyan" : "neutral"}>
                    {kindLabel(item)}
                  </StatusBadge>
                  {item.year ? <span>{item.year}</span> : null}
                  {item.libraryTitle ? <span>{item.libraryTitle}</span> : null}
                </div>
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                aria-label={`${actionLabel}: ${item.title || item.id}`}
                disabled={disabled}
                onClick={() => void onPick(item)}
              >
                {actionLabel}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {results && search.hasMore && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="mt-2"
          disabled={busy}
          onClick={() => void run(search.query, search.fetched)}
        >
          {t("curationSearchMore")}
        </Button>
      )}
    </div>
  );
}

// --- Open subject header -------------------------------------------------------

function SubjectHeader({
  title,
  kind,
  editHref,
  onClose,
}: {
  title: string;
  kind: string;
  editHref?: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <ManageSection>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <h2 className="break-words text-base font-semibold text-[var(--text-primary)]">
            {title}
          </h2>
          <StatusBadge state="cyan">{kind}</StatusBadge>
        </div>
        <div className="flex items-center gap-2">
          {editHref && (
            <Button asChild variant="outline" size="sm">
              <Link to={editHref}>{t("curationEditDetails")}</Link>
            </Button>
          )}
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            {t("curationChooseAnother")}
          </Button>
        </div>
      </div>
    </ManageSection>
  );
}

function LoadState({
  state,
  onClose,
}: {
  state: "loading" | "missing" | "failed";
  onClose: () => void;
}) {
  const { t } = useI18n();
  if (state === "loading") {
    return (
      <ManageSection>
        <EmptyLine message={t("curationLoading")} />
      </ManageSection>
    );
  }
  return (
    <ManageSection>
      {state === "missing" ? (
        <EmptyLine message={t("curationNotFound")} />
      ) : (
        <ErrorLine message={t("curationUnavailable")} />
      )}
      <Button type="button" variant="outline" size="sm" className="mt-3" onClick={onClose}>
        {t("curationChooseAnother")}
      </Button>
    </ManageSection>
  );
}

// --- Media item: files, extras, editions ---------------------------------------

type MediaDetail = {
  title: string;
  files: MediaFileDetail[];
  extras: MediaRelationDetail[];
  editions: MediaRelationDetail[];
};

// The server answers a missing or hidden item with this message (no oracle).
function isUnknownMedia(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.status === 200 &&
    err.message.toLowerCase().includes("unknown media")
  );
}

function MediaView({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useI18n();
  const [detail, setDetail] = useState<MediaDetail | "loading" | "missing" | "failed">("loading");
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);
  const loadSeq = useRef(0);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    const seq = ++loadSeq.current;
    const [media, files, extras, editions] = await Promise.all([
      fetchMedia(id),
      fetchMediaFiles(id),
      fetchMediaRelations(id, "extra-of"),
      fetchMediaRelations(id, "edition-of"),
    ]);
    if (seq !== loadSeq.current) return;
    if (!media) {
      setDetail("missing");
      return;
    }
    const title = media.title || id;
    setDetail({ title, files, extras, editions });
  }, [id]);

  useEffect(() => {
    load().catch((err) => setDetail(isUnknownMedia(err) ? "missing" : "failed"));
  }, [load]);

  if (detail === "loading" || detail === "missing" || detail === "failed") {
    return <LoadState state={detail} onClose={onClose} />;
  }

  // The picked item points AT the open one: from = extra/edition, to = this
  // item. That is the direction scan writes and mediaRelations reads.
  const link = async (relation: "extra-of" | "edition-of", picked: SearchResultItem) => {
    if (inFlight.current) return false;
    inFlight.current = true;
    setSaveError("");
    setSaving(true);
    try {
      await linkMediaRelation(picked.id, relation, id);
      await load();
      return true;
    } catch {
      setSaveError(t("curationSaveFailed"));
      return false;
    } finally {
      inFlight.current = false;
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <SubjectHeader
        title={detail.title}
        kind={t("curationKindMedia")}
        editHref={`/manage/items/${encodeURIComponent(id)}`}
        onClose={onClose}
      />
      <ErrorLine message={saveError} />

      <ManageSection
        title={t("curationFilesHeading")}
        lead={t("curationFilesLead")}
        actions={<StatusBadge state="neutral">{detail.files.length}</StatusBadge>}
      >
        {detail.files.length === 0 ? (
          <EmptyLine message={t("curationNoFiles")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("curationVersionLabel")}</TableHead>
                <TableHead>{t("curationPartOrdinal")}</TableHead>
                <TableHead>{t("curationFilePath")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {detail.files.map((file) => (
                <TableRow key={`${file.versionLabel}-${file.partOrdinal}-${file.path}`}>
                  <TableCell className="manage-cell-primary">
                    {file.versionLabel || t("curationDefaultVersion")}
                  </TableCell>
                  <TableCell className="manage-cell-num text-[var(--text-muted)]">
                    {file.partOrdinal}
                  </TableCell>
                  <TableCell className="manage-cell-num whitespace-normal break-all">
                    {file.path}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        )}
      </ManageSection>

      <LinkedMediaSection
        id="curation-extra"
        title={t("curationExtrasHeading")}
        lead={t("curationExtrasLead")}
        empty={t("curationNoExtras")}
        addLabel={t("curationAddExtra")}
        actionLabel={t("curationLinkAsExtra")}
        rows={detail.extras}
        subjectID={id}
        saving={saving}
        onPick={(picked) => link("extra-of", picked)}
      />
      <LinkedMediaSection
        id="curation-edition"
        title={t("curationEditionsHeading")}
        lead={t("curationEditionsLead")}
        empty={t("curationNoEditions")}
        addLabel={t("curationAddEdition")}
        actionLabel={t("curationLinkAsEdition")}
        rows={detail.editions}
        subjectID={id}
        saving={saving}
        onPick={(picked) => link("edition-of", picked)}
      />
    </div>
  );
}

function LinkedMediaSection({
  id,
  title,
  lead,
  empty,
  addLabel,
  actionLabel,
  rows,
  subjectID,
  saving,
  onPick,
}: {
  id: string;
  title: string;
  lead: string;
  empty: string;
  addLabel: string;
  actionLabel: string;
  rows: MediaRelationDetail[];
  subjectID: string;
  saving: boolean;
  onPick: (picked: SearchResultItem) => Promise<boolean>;
}) {
  const { t } = useI18n();
  const [adding, setAdding] = useState(false);
  const excludeIDs = new Set([subjectID, ...rows.map((r) => r.media.id)]);
  return (
    <ManageSection
      title={title}
      lead={lead}
      actions={
        <>
          <StatusBadge state="neutral">{rows.length}</StatusBadge>
          {adding ? (
            <Button type="button" size="sm" variant="ghost" onClick={() => setAdding(false)}>
              {t("cancel")}
            </Button>
          ) : (
            <Button type="button" size="sm" variant="outline" onClick={() => setAdding(true)}>
              {addLabel}
            </Button>
          )}
        </>
      }
    >
      {rows.length === 0 ? (
        <EmptyLine message={empty} />
      ) : (
        <ul className="divide-y divide-[var(--line-soft)]">
          {rows.map((row) => (
            <li key={row.media.id} className="py-2 text-sm">
              <Link
                className="manage-cell-primary hover:underline"
                to={`/manage/curation?open=${encodeURIComponent(row.media.id)}&as=media`}
              >
                {row.media.title || row.media.id}
              </Link>
              {row.media.year ? (
                <span className="ml-2 text-[var(--text-muted)]">{row.media.year}</span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {adding && (
        <div className="mt-3 border-t border-[var(--line-soft)] pt-3">
          <ItemSearch
            id={id}
            label={addLabel}
            actionLabel={actionLabel}
            mediaOnly
            excludeID={subjectID}
            excludeIDs={excludeIDs}
            disabled={saving}
            onPick={async (picked) => {
              if (await onPick(picked)) setAdding(false);
            }}
          />
        </div>
      )}
    </ManageSection>
  );
}

// --- Entity: ordered linked items ----------------------------------------------

type EntityDetail = { title: string; kind: string; rows: EntityRelationDetail[] };

function targetID(row: EntityRelationDetail): string {
  return (row.targetKind === "entity" ? row.entity?.id : row.media?.id) ?? "";
}

// entityRelations pages at most 200 rows and refuses offsets past 10,000; the
// list reads until a short page.
const RELATION_PAGE = 200;
const RELATION_OFFSET_MAX = 10_000;

async function fetchAllEntityRelations(id: string): Promise<EntityRelationDetail[]> {
  const rows: EntityRelationDetail[] = [];
  for (let offset = 0; offset <= RELATION_OFFSET_MAX; offset += RELATION_PAGE) {
    const page = await fetchEntityRelations(id, null, { limit: RELATION_PAGE, offset });
    rows.push(...page);
    if (page.length < RELATION_PAGE) break;
  }
  return rows;
}

function isUnknownEntity(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.status === 200 &&
    err.message.toLowerCase().includes("unknown entity")
  );
}

/** A position past any relation's end; the server places the row last. */
const APPEND_POSITION = 2_147_483_647;

function EntityView({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useI18n();
  const [detail, setDetail] = useState<EntityDetail | "loading" | "missing" | "failed">("loading");
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);
  const [adding, setAdding] = useState(false);
  const [newRelation, setNewRelation] = useState("");
  // Only the latest load may set state, so a slow earlier read never
  // overwrites a newer one.
  const loadSeq = useRef(0);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    const seq = ++loadSeq.current;
    const entity = await fetchEntity(id);
    if (seq !== loadSeq.current) return;
    if (!entity) {
      setDetail("missing");
      return;
    }
    const rows = await fetchAllEntityRelations(id);
    if (seq !== loadSeq.current) return;
    setDetail({ title: entity.title || id, kind: entity.kind, rows });
  }, [id]);

  useEffect(() => {
    load().catch((err) => setDetail(isUnknownEntity(err) ? "missing" : "failed"));
  }, [load]);

  if (typeof detail === "string") return <LoadState state={detail} onClose={onClose} />;

  const rows = detail.rows;
  // Each row's place within its own relation, and each relation's size, in
  // one pass over the loaded rows.
  const relationSize = new Map<string, number>();
  const positionOf = new Map<EntityRelationDetail, number>();
  for (const row of rows) {
    const n = relationSize.get(row.relation) ?? 0;
    positionOf.set(row, n);
    relationSize.set(row.relation, n + 1);
  }
  const relationNames = [...relationSize.keys()];
  const relation = newRelation.trim() || relationNames[0] || "";

  // One save at a time. The server places the row and renumbers the whole
  // relation in one write; the reload then shows what it holds. A reload that
  // fails leaves nothing stale to edit: the view falls back to "unavailable".
  const save = async (write: () => Promise<void>) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setSaveError("");
    setSaving(true);
    try {
      await write();
    } catch {
      setSaveError(t("curationSaveFailed"));
    }
    try {
      await load();
    } catch {
      setDetail("failed");
    } finally {
      inFlight.current = false;
      setSaving(false);
    }
  };

  // Moves a row one place within its own relation.
  const move = (row: EntityRelationDetail, dir: -1 | 1) =>
    save(async () => {
      const to = (positionOf.get(row) ?? 0) + dir;
      if (to < 0 || to >= (relationSize.get(row.relation) ?? 0)) return;
      await upsertEntityRelation({
        fromEntityID: id,
        relation: row.relation,
        toEntityID: row.targetKind === "entity" ? targetID(row) : null,
        toMediaID: row.targetKind === "media" ? targetID(row) : null,
        position: to,
      });
    });

  // New items go after the last one in the chosen relation; a position past
  // the end appends.
  const add = async (picked: SearchResultItem) => {
    if (!relation) return;
    if (rows.some((row) => row.relation === relation && targetID(row) === picked.id)) {
      setSaveError(t("curationAlreadyMember"));
      return;
    }
    await save(async () => {
      await upsertEntityRelation({
        fromEntityID: id,
        relation,
        toEntityID: isEntity(picked) ? picked.id : null,
        toMediaID: isEntity(picked) ? null : picked.id,
        // Past the end: the server appends after the last item it holds now,
        // not after the last one this page loaded.
        position: APPEND_POSITION,
      });
      setAdding(false);
      setNewRelation("");
    });
  };

  return (
    <div className="space-y-4">
      <SubjectHeader
        title={detail.title}
        kind={detail.kind || t("curationKindEntity")}
        onClose={onClose}
      />
      <ErrorLine message={saveError} />
      <ManageSection
        title={t("curationMembersHeading")}
        lead={t("curationMembersLead")}
        actions={
          <>
            <StatusBadge state="neutral">{rows.length}</StatusBadge>
            {adding ? (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => {
                  setAdding(false);
                  setNewRelation("");
                }}
              >
                {t("cancel")}
              </Button>
            ) : (
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={saving}
                onClick={() => setAdding(true)}
              >
                {t("curationAddMember")}
              </Button>
            )}
          </>
        }
      >
        {rows.length === 0 ? (
          <EmptyLine message={t("curationNoMembers")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead className="w-24">{t("curationRelationOrder")}</TableHead>
                <TableHead>{t("curationRelationTarget")}</TableHead>
                <TableHead>{t("curationRelationName")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row, i) => {
                const position = positionOf.get(row) ?? 0;
                const last = (relationSize.get(row.relation) ?? 0) - 1;
                const name =
                  (row.targetKind === "entity" ? row.entity?.title : row.media?.title) ||
                  targetID(row);
                return (
                  <TableRow key={`${row.relation}-${targetID(row) || i}`}>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <span className="manage-cell-num w-6 text-[var(--text-muted)]">
                          {position + 1}
                        </span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          aria-label={`${t("curationRelationMoveUp")}: ${name}`}
                          disabled={saving || position === 0}
                          onClick={() => void move(row, -1)}
                        >
                          <ArrowUp className="size-4" aria-hidden />
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          aria-label={`${t("curationRelationMoveDown")}: ${name}`}
                          disabled={saving || position === last}
                          onClick={() => void move(row, 1)}
                        >
                          <ArrowDown className="size-4" aria-hidden />
                        </Button>
                      </div>
                    </TableCell>
                    <TableCell className="manage-cell-primary">
                      {row.targetKind === "media" && row.media ? (
                        <Link
                          className="hover:underline"
                          to={`/manage/curation?open=${encodeURIComponent(row.media.id)}&as=media`}
                        >
                          {name}
                        </Link>
                      ) : row.entity ? (
                        <Link
                          className="hover:underline"
                          to={`/manage/curation?open=${encodeURIComponent(row.entity.id)}&as=entity`}
                        >
                          {name}
                        </Link>
                      ) : (
                        name
                      )}
                    </TableCell>
                    <TableCell>
                      <StatusBadge state="neutral">{row.relation}</StatusBadge>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </ManageTable>
        )}
        {adding && (
          <div className="mt-3 space-y-3 border-t border-[var(--line-soft)] pt-3">
            <div className="max-w-xs space-y-1">
              <Label htmlFor="curation-member-relation">{t("curationRelationName")}</Label>
              {relationNames.length > 0 && (
                <Select value={relation} onValueChange={setNewRelation}>
                  <SelectTrigger id="curation-member-relation">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {relationNames.map((name) => (
                      <SelectItem key={name} value={name}>
                        {name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
              <Input
                id={relationNames.length === 0 ? "curation-member-relation" : undefined}
                aria-label={t("curationRelationName")}
                value={newRelation}
                placeholder={relationNames[0] || undefined}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setNewRelation(e.target.value)}
              />
              {relationNames.length === 0 && (
                <p className="text-xs text-[var(--text-muted)]">{t("curationRelationNameHint")}</p>
              )}
            </div>
            {relation ? (
              <ItemSearch
                id="curation-member"
                label={t("curationAddMember")}
                actionLabel={t("curationAddToGroup")}
                excludeID={id}
                disabled={saving}
                onPick={add}
              />
            ) : null}
          </div>
        )}
      </ManageSection>
    </div>
  );
}
