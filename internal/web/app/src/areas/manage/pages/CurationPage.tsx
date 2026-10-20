import { useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
} from "@/components/beamfall/workbench";
import { Tabs } from "@/components/ui/astryx-primitives";
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
import {
  fetchEntity,
  fetchEntityRelations,
  fetchMediaFields,
  fetchMediaFiles,
  fetchMediaRelations,
  linkMediaRelation,
  upsertEntityRelation,
} from "@/lib/graphql";
import type {
  EntityFieldInfo,
  EntityRelationDetail,
  MediaFileDetail,
  MediaRelationDetail,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";

type SubjectType = "media" | "entity";

// A loaded curation subject: a Media or Entity, with the substrate's
// reveal-filtered reads materialized. Relations are only meaningful for an
// Entity (entityRelations); files/extras only for a Media. The wire entity is
// Media; "kind" is a plugin display discriminant, never a schema type
// (HARD invariant #1/#2).
type Subject = {
  type: SubjectType;
  id: string;
  title: string;
  kind: string;
  fields: EntityFieldInfo[];
  relations: EntityRelationDetail[];
  files: MediaFileDetail[];
  extras: MediaRelationDetail[];
};

// Manage Curation editor (WEB-5B, ADR-0001/0005/0061): the generic,
// domain-agnostic curation surface. Open a Media or Entity by ID and review its
// plugin-declared fields, the ordered relation rows (reorderable via
// upsertEntityRelation), the backing Versions/Parts (mediaFiles), and the
// Extras/Editions relations (mediaRelations / linkMediaRelation). Every write is
// manager-gated and reveal-filtered server-side; this surface is only an
// affordance. Server strings render as React text only (never HTML, ADR-0012 §5).
export function CurationPage() {
  const t = useT();
  const [subjectType, setSubjectType] = useState<SubjectType>("media");
  const [subjectID, setSubjectID] = useState("");
  const [subject, setSubject] = useState<Subject | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");

  const load = async (type: SubjectType, id: string) => {
    setError("");
    if (type === "entity") {
      const entity = await fetchEntity(id);
      if (!entity) {
        setSubject(null);
        setLoaded(true);
        return;
      }
      const relations = await fetchEntityRelations(id);
      setSubject({
        type,
        id,
        title: entity.title,
        kind: entity.kind,
        fields: entity.fields,
        relations,
        files: [],
        extras: [],
      });
      setLoaded(true);
      return;
    }
    // Media subject: fields + backing files + Extras/Editions relations.
    const [fields, files, extras, editions] = await Promise.all([
      fetchMediaFields(id),
      fetchMediaFiles(id),
      fetchMediaRelations(id, "extra-of"),
      fetchMediaRelations(id, "edition-of"),
    ]);
    const title = fields.find((f) => f.name === "title")?.value ?? id;
    setSubject({
      type,
      id,
      title,
      kind: "",
      fields,
      relations: [],
      files,
      extras: [...extras, ...editions],
    });
    setLoaded(true);
  };

  const onLoad = async () => {
    const id = subjectID.trim();
    if (!id) return;
    try {
      await load(subjectType, id);
    } catch {
      setSubject(null);
      setLoaded(true);
      setError(t("curationUnavailable"));
    }
  };

  const reload = () => {
    if (subject)
      void load(subject.type, subject.id).catch(() => setError(t("curationUnavailable")));
  };

  return (
    <div className="space-y-4">
      <ManageSection title={t("curationHeading")} lead={t("curationLead")}>
        <div className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <Label htmlFor="curation-type">{t("curationSubjectType")}</Label>
            <Select value={subjectType} onValueChange={(v) => setSubjectType(v as SubjectType)}>
              <SelectTrigger id="curation-type" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="media">{t("curationSubjectMedia")}</SelectItem>
                <SelectItem value="entity">{t("curationSubjectEntity")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="grow space-y-1">
            <Label htmlFor="curation-id">{t("curationSubjectID")}</Label>
            <Input
              id="curation-id"
              value={subjectID}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setSubjectID(e.target.value)}
            />
          </div>
          <Button type="button" variant="outline" onClick={onLoad} disabled={!subjectID.trim()}>
            {t("fieldLocksLoad")}
          </Button>
        </div>
        <ErrorLine message={error} />
        {loaded && !subject && (
          <p className="mt-3 text-sm text-muted-foreground">{t("curationNotFound")}</p>
        )}
        {!loaded && <EmptyLine message={t("curationEmpty")} />}
      </ManageSection>

      {subject && (
        <>
          <FieldsSection subject={subject} />
          <RelationsSection subject={subject} onError={setError} onChange={reload} />
          <FilesSection subject={subject} onError={setError} onChange={reload} />
        </>
      )}
    </div>
  );
}

// --- Fields ---------------------------------------------------------------

function FieldsSection({ subject }: { subject: Subject }) {
  const t = useT();
  return (
    <ManageSection
      title={t("curationFieldsHeading")}
      actions={
        <StatusBadge state="cyan">
          {subject.kind ||
            t(subject.type === "media" ? "curationSubjectMedia" : "curationSubjectEntity")}
        </StatusBadge>
      }
    >
      <div className="mb-2 text-sm font-bold text-[var(--text-primary)]">{subject.title}</div>
      {subject.fields.length === 0 ? (
        <EmptyLine message={t("curationNoFields")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("curationFieldName")}</TableHead>
              <TableHead>{t("curationFieldType")}</TableHead>
              <TableHead>{t("curationFieldValue")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {subject.fields.map((field) => (
              <TableRow key={field.name}>
                <TableCell className="manage-cell-primary">{field.name}</TableCell>
                <TableCell className="text-[var(--text-muted)] manage-cell-num">
                  {field.type}
                </TableCell>
                <TableCell className="text-[var(--text-muted)]">{field.value}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </ManageTable>
      )}
    </ManageSection>
  );
}

// --- Ordered relations (Entity subjects) ----------------------------------

function RelationsSection({
  subject,
  onError,
  onChange,
}: {
  subject: Subject;
  onError: (message: string) => void;
  onChange: () => void;
}) {
  const t = useT();
  const [relation, setRelation] = useState("");
  const [targetID, setTargetID] = useState("");
  const [order, setOrder] = useState("");

  if (subject.type !== "entity") {
    return (
      <ManageSection title={t("curationRelationsHeading")}>
        <EmptyLine message={t("curationRelationsEntityOnly")} />
      </ManageSection>
    );
  }

  // Reorder swaps this row's sortOrder with its neighbour by re-upserting both,
  // keyed by relation+target. The server is the order authority; we reload after.
  const move = async (index: number, dir: -1 | 1) => {
    onError("");
    const rows = subject.relations;
    const a = rows[index];
    const b = rows[index + dir];
    if (!a || !b) return;
    const aOrder = a.sortOrder ?? index;
    const bOrder = b.sortOrder ?? index + dir;
    try {
      await upsertEntityRelation({
        fromEntityID: subject.id,
        relation: a.relation,
        toEntityID: a.targetKind === "entity" ? a.entity?.id : null,
        toMediaID: a.targetKind === "media" ? a.media?.id : null,
        sortOrder: bOrder,
      });
      await upsertEntityRelation({
        fromEntityID: subject.id,
        relation: b.relation,
        toEntityID: b.targetKind === "entity" ? b.entity?.id : null,
        toMediaID: b.targetKind === "media" ? b.media?.id : null,
        sortOrder: aOrder,
      });
      onChange();
    } catch {
      onError(t("curationUnavailable"));
    }
  };

  const onAdd = async () => {
    onError("");
    const rel = relation.trim();
    const target = targetID.trim();
    if (!rel || !target) return;
    const parsed = order.trim() === "" ? null : Number(order.trim());
    try {
      await upsertEntityRelation({
        fromEntityID: subject.id,
        relation: rel,
        toMediaID: target,
        sortOrder: parsed,
      });
      setTargetID("");
      setOrder("");
      onChange();
    } catch {
      onError(t("curationUnavailable"));
    }
  };

  const targetLabel = (row: EntityRelationDetail): { text: string; kind: string } => {
    if (row.targetKind === "entity") {
      return { text: row.entity?.title ?? row.entity?.id ?? "", kind: t("curationKindEntity") };
    }
    return { text: row.media?.title ?? row.media?.id ?? "", kind: t("curationKindMedia") };
  };

  return (
    <ManageSection
      title={t("curationRelationsHeading")}
      actions={<StatusBadge state="neutral">{subject.relations.length}</StatusBadge>}
    >
      {subject.relations.length === 0 ? (
        <EmptyLine message={t("curationNoRelations")} />
      ) : (
        <ManageTable>
          <TableHeader>
            <TableRow>
              <TableHead>{t("curationRelationOrder")}</TableHead>
              <TableHead>{t("curationRelationName")}</TableHead>
              <TableHead>{t("curationRelationTarget")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {subject.relations.map((row, i) => {
              const label = targetLabel(row);
              return (
                <TableRow key={`${row.relation}-${row.entity?.id ?? row.media?.id ?? i}`}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <div className="flex flex-col">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={t("curationRelationMoveUp")}
                          disabled={i === 0}
                          onClick={() => move(i, -1)}
                          className="h-4 px-1 leading-none"
                        >
                          ▲
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={t("curationRelationMoveDown")}
                          disabled={i === subject.relations.length - 1}
                          onClick={() => move(i, 1)}
                          className="h-4 px-1 leading-none"
                        >
                          ▼
                        </Button>
                      </div>
                      <span className="manage-cell-num text-[var(--text-muted)]">
                        {row.sortOrder ?? i}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="manage-cell-num">{row.relation}</TableCell>
                  <TableCell>
                    <span className="manage-cell-primary">{label.text}</span>{" "}
                    <StatusBadge state="neutral">{label.kind}</StatusBadge>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </ManageTable>
      )}

      <div className="mt-3 flex flex-wrap items-end gap-3 border-t border-[var(--line-soft)] pt-3">
        <div className="space-y-1">
          <Label htmlFor="curation-rel-name">{t("curationRelationName")}</Label>
          <Input
            id="curation-rel-name"
            value={relation}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setRelation(e.target.value)}
            className="w-32"
          />
        </div>
        <div className="grow space-y-1">
          <Label htmlFor="curation-rel-target">{t("curationTargetMediaID")}</Label>
          <Input
            id="curation-rel-target"
            value={targetID}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setTargetID(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="curation-rel-order">{t("curationRelationOrder")}</Label>
          <Input
            id="curation-rel-order"
            type="number"
            value={order}
            onChange={(e) => setOrder(e.target.value)}
            className="w-20"
          />
        </div>
        <Button type="button" onClick={onAdd} disabled={!relation.trim() || !targetID.trim()}>
          {t("curationAddRelation")}
        </Button>
      </div>
    </ManageSection>
  );
}

// --- Versions / Parts / Extras (Media subjects) ---------------------------

function FilesSection({
  subject,
  onError,
  onChange,
}: {
  subject: Subject;
  onError: (message: string) => void;
  onChange: () => void;
}) {
  const t = useT();
  const [tab, setTab] = useState<"versions" | "extras">("versions");
  const [relation, setRelation] = useState<"extra-of" | "edition-of">("extra-of");
  const [extraID, setExtraID] = useState("");

  if (subject.type !== "media") {
    return (
      <ManageSection title={t("curationFilesHeading")}>
        <EmptyLine message={t("curationFilesMediaOnly")} />
      </ManageSection>
    );
  }

  const onLink = async () => {
    onError("");
    const id = extraID.trim();
    if (!id) return;
    try {
      await linkMediaRelation(subject.id, relation, id);
      setExtraID("");
      onChange();
    } catch {
      onError(t("curationUnavailable"));
    }
  };

  return (
    <Tabs.Root value={tab} onValueChange={(value) => setTab(value as "versions" | "extras")}>
      <ManageSection
        title={t("curationFilesHeading")}
        actions={
          <Tabs.List aria-label={t("curationFilesHeading")} className="flex gap-1">
            <Tabs.Tab
              value="versions"
              render={
                <Button
                  type="button"
                  variant={tab === "versions" ? "default" : "outline"}
                  size="sm"
                />
              }
            >
              {t("curationTabVersions")}
            </Tabs.Tab>
            <Tabs.Tab
              value="extras"
              render={
                <Button
                  type="button"
                  variant={tab === "extras" ? "default" : "outline"}
                  size="sm"
                />
              }
            >
              {t("curationTabExtras")}
            </Tabs.Tab>
          </Tabs.List>
        }
      >
        <Tabs.Panel value="versions">
          {subject.files.length === 0 ? (
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
                {subject.files.map((file) => (
                  <TableRow key={`${file.versionLabel}-${file.partOrdinal}-${file.path}`}>
                    <TableCell className="manage-cell-primary">{file.versionLabel}</TableCell>
                    <TableCell className="manage-cell-num text-[var(--text-muted)]">
                      {file.partOrdinal}
                    </TableCell>
                    <TableCell className="manage-cell-num text-[var(--text-muted)]">
                      {file.path}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </ManageTable>
          )}
        </Tabs.Panel>
        <Tabs.Panel value="extras">
          {subject.extras.length === 0 ? (
            <EmptyLine message={t("curationNoExtras")} />
          ) : (
            <ManageTable>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("curationExtraRelation")}</TableHead>
                  <TableHead>{t("curationRelationTarget")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {subject.extras.map((row, i) => (
                  <TableRow key={`${row.relation}-${row.media.id}-${i}`}>
                    <TableCell className="manage-cell-num">{row.relation}</TableCell>
                    <TableCell className="manage-cell-primary">
                      {row.media.title || row.media.id}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </ManageTable>
          )}
        </Tabs.Panel>

        <div className="mt-3 flex flex-wrap items-end gap-3 border-t border-[var(--line-soft)] pt-3">
          <div className="space-y-1">
            <Label htmlFor="curation-extra-rel">{t("curationExtraRelation")}</Label>
            <Select
              value={relation}
              onValueChange={(v) => setRelation(v as "extra-of" | "edition-of")}
            >
              <SelectTrigger id="curation-extra-rel" className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="extra-of">extra-of</SelectItem>
                <SelectItem value="edition-of">edition-of</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="grow space-y-1">
            <Label htmlFor="curation-extra-id">{t("curationExtraMediaID")}</Label>
            <Input
              id="curation-extra-id"
              value={extraID}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setExtraID(e.target.value)}
            />
          </div>
          <Button type="button" onClick={onLink} disabled={!extraID.trim()}>
            {t("curationLinkExtra")}
          </Button>
        </div>
      </ManageSection>
    </Tabs.Root>
  );
}
