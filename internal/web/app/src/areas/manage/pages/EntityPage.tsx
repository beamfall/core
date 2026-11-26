import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { EntityPicker } from "@/components/beamfall/EntityPicker";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
  TableSkeletonRows,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchEntity } from "@/lib/graphql";
import type { EntityInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";

function humanizeField(name: string): string {
  return name.replace(/([a-z])([A-Z])/g, "$1 $2");
}

// Entity manager (ADR-0007 §3): open an entity by ID and review its declared
// fields (server-stringified values — generic, plugin-named, never domain
// schema). The wire entity is Media (HARD invariant #1); `kind` is a plugin
// display discriminant. Ported from app.js openEntity (the detail header). The
// per-Media curation surfaces — artwork candidates and field locks — are their
// own routes keyed by the same Media ID.
export function EntityPage() {
  const t = useT();
  const [entityID, setEntityID] = useState("");
  const [entity, setEntity] = useState<EntityInfo | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  // Only the newest load may land, so a slow earlier pick cannot replace it.
  const loadSeq = useRef(0);
  const copyTimerRef = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(copyTimerRef.current), []);

  const onLoad = async (picked?: string) => {
    setError("");
    setCopied(false);
    const id = (picked ?? entityID).trim();
    if (!id) return;
    const seq = ++loadSeq.current;
    setLoading(true);
    try {
      const result = await fetchEntity(id);
      if (seq !== loadSeq.current) return;
      setEntity(result);
      setLoaded(true);
    } catch (err) {
      if (seq !== loadSeq.current) return;
      setEntity(null);
      setLoaded(false);
      setError(err instanceof Error && err.message ? err.message : t("curationUnavailable"));
    } finally {
      if (seq === loadSeq.current) setLoading(false);
    }
  };

  return (
    <ManageSection>
      <div className="space-y-1">
        <Label htmlFor="entity-id">{t("tagTargetEntity")}</Label>
        <EntityPicker
          id="entity-id"
          kind="entity"
          value={entityID}
          onChange={(id) => {
            setEntityID(id);
            if (id) void onLoad(id);
            else {
              loadSeq.current++;
              setEntity(null);
              setLoaded(false);
              setLoading(false);
              setError("");
            }
          }}
        />
      </div>

      <div className="mt-4">
        {loading ? (
          <ManageTable>
            <TableBody>
              <TableSkeletonRows columns={2} rows={3} />
            </TableBody>
          </ManageTable>
        ) : !loaded ? (
          !error && <EmptyLine message={t("manageSelectEntityPrompt")} />
        ) : !entity ? (
          <EmptyLine message={t("curationNotFound")} />
        ) : (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <h3 className="text-sm font-semibold text-[var(--text-primary)]">{entity.title}</h3>
                <StatusBadge state="neutral">{entity.kind}</StatusBadge>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    void navigator.clipboard
                      ?.writeText(entity.id)
                      .then(() => {
                        setCopied(true);
                        window.clearTimeout(copyTimerRef.current);
                        copyTimerRef.current = window.setTimeout(() => setCopied(false), 2000);
                      })
                      .catch(() => {});
                  }}
                >
                  {copied ? t("backupKeyCopied") : entity.id}
                </Button>
                <Button asChild variant="outline" size="sm">
                  <Link to={`/manage/curation?open=${encodeURIComponent(entity.id)}&as=entity`}>
                    {t("curationOpen")}
                  </Link>
                </Button>
              </div>
            </div>
            {entity.fields.length === 0 ? (
              <EmptyLine message={t("none")} />
            ) : (
              <ManageTable>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("bulkEditFieldName")}</TableHead>
                    <TableHead>{t("bulkEditFieldValue")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {entity.fields.map((field) => (
                    <TableRow key={field.name}>
                      <TableCell className="manage-cell-primary">
                        {humanizeField(field.name)}
                      </TableCell>
                      <TableCell className="text-[var(--text-muted)]">{field.value}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </ManageTable>
            )}
          </div>
        )}
      </div>
      <ErrorLine message={error} />
    </ManageSection>
  );
}
