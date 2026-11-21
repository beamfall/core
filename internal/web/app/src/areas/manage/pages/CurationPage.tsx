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
import { displayString } from "@/lib/display";
import {
  fetchEntity,
  fetchEntityRelations,
  fetchMediaFields,
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
    setParams({ open: item.id, as: item.entityKind ? "entity" : "media" });
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

// Keyword catalog search (reveal-filtered server-side) with one action per
// result. `mediaOnly` hides Entity rows where only a Media item can be linked.
function ItemSearch({
  id,
  label,
  actionLabel,
  onPick,
  mediaOnly = false,
  excludeID,
}: {
  id: string;
  label: string;
  actionLabel: string;
  onPick: (item: SearchResultItem) => void | Promise<void>;
  mediaOnly?: boolean;
  excludeID?: string;
}) {
  const { t, displayLocale } = useI18n();
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<SearchResultItem[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const request = useRef<AbortController | null>(null);

  useEffect(() => () => request.current?.abort(), []);

  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    const query = term.trim();
    if (!query) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setError("");
    setBusy(true);
    try {
      const response = await searchResult(query, "keyword", controller.signal, { offset: 0 });
      if (controller.signal.aborted) return;
      setResults(
        response.items.filter((item) => item.id !== excludeID && !(mediaOnly && item.entityKind)),
      );
    } catch {
      if (controller.signal.aborted) return;
      setResults(null);
      setError(t("curationSearchFailed"));
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  };

  const kindLabel = (item: SearchResultItem) => {
    if (item.entityKind) {
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
      {results && results.length === 0 && <EmptyLine message={t("curationSearchNoResults")} />}
      {results && results.length > 0 && (
        <ul className="mt-3 divide-y divide-[var(--line-soft)] rounded-md border border-[var(--line-soft)]">
          {results.map((item) => (
            <li key={item.id} className="flex items-center justify-between gap-3 px-3 py-2">
              <div className="min-w-0">
                <div className="break-words text-sm font-medium text-[var(--text-primary)]">
                  {item.title || item.id}
                </div>
                <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--text-muted)]">
                  <StatusBadge state={item.entityKind ? "cyan" : "neutral"}>
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
                onClick={() => void onPick(item)}
              >
                {actionLabel}
              </Button>
            </li>
          ))}
        </ul>
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
  if (state === "loading") return <EmptyLine message={t("curationLoading")} />;
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

function MediaView({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useI18n();
  const [detail, setDetail] = useState<MediaDetail | "loading" | "failed">("loading");
  const [saveError, setSaveError] = useState("");

  const load = useCallback(async () => {
    const [fields, files, extras, editions] = await Promise.all([
      fetchMediaFields(id),
      fetchMediaFiles(id),
      fetchMediaRelations(id, "extra-of"),
      fetchMediaRelations(id, "edition-of"),
    ]);
    const title = fields.find((f) => f.name === "title")?.value || id;
    setDetail({ title, files, extras, editions });
  }, [id]);

  useEffect(() => {
    load().catch(() => setDetail("failed"));
  }, [load]);

  if (detail === "loading" || detail === "failed") {
    return <LoadState state={detail} onClose={onClose} />;
  }

  // The picked item points AT the open one: from = extra/edition, to = this
  // item. That is the direction scan writes and mediaRelations reads.
  const link = async (relation: "extra-of" | "edition-of", picked: SearchResultItem) => {
    setSaveError("");
    try {
      await linkMediaRelation(picked.id, relation, id);
      await load();
    } catch {
      setSaveError(t("curationSaveFailed"));
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
                  <TableCell className="manage-cell-num break-all text-[var(--text-muted)]">
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
  onPick: (picked: SearchResultItem) => Promise<void>;
}) {
  const [adding, setAdding] = useState(false);
  return (
    <ManageSection
      title={title}
      lead={lead}
      actions={
        <>
          <StatusBadge state="neutral">{rows.length}</StatusBadge>
          {!adding && (
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
            onPick={async (picked) => {
              await onPick(picked);
              setAdding(false);
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

function EntityView({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useI18n();
  const [detail, setDetail] = useState<EntityDetail | "loading" | "missing" | "failed">("loading");
  const [saveError, setSaveError] = useState("");
  const [adding, setAdding] = useState(false);
  const [newRelation, setNewRelation] = useState("");

  const load = useCallback(async () => {
    const entity = await fetchEntity(id);
    if (!entity) {
      setDetail("missing");
      return;
    }
    const rows = await fetchEntityRelations(id);
    setDetail({ title: entity.title || id, kind: entity.kind, rows });
  }, [id]);

  useEffect(() => {
    load().catch(() => setDetail("failed"));
  }, [load]);

  if (typeof detail === "string") return <LoadState state={detail} onClose={onClose} />;

  const rows = detail.rows;
  const relationNames = [...new Set(rows.map((row) => row.relation))];
  const relation = newRelation.trim() || relationNames[0] || "";

  // Reorder swaps this row's sortOrder with its neighbour by re-upserting both,
  // keyed by relation+target. The server is the order authority; reload after.
  const move = async (index: number, dir: -1 | 1) => {
    setSaveError("");
    const a = rows[index];
    const b = rows[index + dir];
    if (!a || !b) return;
    const aOrder = a.sortOrder ?? index;
    const bOrder = b.sortOrder ?? index + dir;
    try {
      for (const [row, sortOrder] of [
        [a, bOrder],
        [b, aOrder],
      ] as const) {
        await upsertEntityRelation({
          fromEntityID: id,
          relation: row.relation,
          toEntityID: row.targetKind === "entity" ? targetID(row) : null,
          toMediaID: row.targetKind === "media" ? targetID(row) : null,
          sortOrder,
        });
      }
      await load();
    } catch {
      setSaveError(t("curationSaveFailed"));
    }
  };

  // New items go after the last one.
  const add = async (picked: SearchResultItem) => {
    setSaveError("");
    if (!relation) return;
    const last = rows.reduce((max, row, i) => Math.max(max, row.sortOrder ?? i), -1);
    try {
      await upsertEntityRelation({
        fromEntityID: id,
        relation,
        toEntityID: picked.entityKind ? picked.id : null,
        toMediaID: picked.entityKind ? null : picked.id,
        sortOrder: last + 1,
      });
      setAdding(false);
      setNewRelation("");
      await load();
    } catch {
      setSaveError(t("curationSaveFailed"));
    }
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
            {!adding && (
              <Button type="button" size="sm" variant="outline" onClick={() => setAdding(true)}>
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
                const name =
                  (row.targetKind === "entity" ? row.entity?.title : row.media?.title) ||
                  targetID(row);
                return (
                  <TableRow key={`${row.relation}-${targetID(row) || i}`}>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <span className="manage-cell-num w-6 text-[var(--text-muted)]">
                          {i + 1}
                        </span>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          aria-label={`${t("curationRelationMoveUp")}: ${name}`}
                          disabled={i === 0}
                          onClick={() => void move(i, -1)}
                        >
                          <ArrowUp className="size-4" aria-hidden />
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          aria-label={`${t("curationRelationMoveDown")}: ${name}`}
                          disabled={i === rows.length - 1}
                          onClick={() => void move(i, 1)}
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
            {relationNames.length > 1 && (
              <div className="max-w-xs space-y-1">
                <Label htmlFor="curation-member-relation">{t("curationRelationName")}</Label>
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
              </div>
            )}
            {relationNames.length === 0 && (
              <div className="max-w-xs space-y-1">
                <Label htmlFor="curation-member-relation">{t("curationRelationName")}</Label>
                <Input
                  id="curation-member-relation"
                  value={newRelation}
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => setNewRelation(e.target.value)}
                />
                <p className="text-xs text-[var(--text-muted)]">{t("curationRelationNameHint")}</p>
              </div>
            )}
            {relation ? (
              <ItemSearch
                id="curation-member"
                label={t("curationAddMember")}
                actionLabel={t("curationAddToGroup")}
                excludeID={id}
                onPick={add}
              />
            ) : null}
          </div>
        )}
      </ManageSection>
    </div>
  );
}
