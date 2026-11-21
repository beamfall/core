import { useRef, useState } from "react";
import { EntityPicker } from "@/components/beamfall/EntityPicker";
import {
  EmptyLine,
  ErrorLine,
  ManageSection,
  ManageTable,
  StatusBadge,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { fetchEntity } from "@/lib/graphql";
import type { EntityInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";

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
  const [error, setError] = useState("");
  // Only the newest load may land, so a slow earlier pick cannot replace it.
  const loadSeq = useRef(0);

  const onLoad = async (picked?: string) => {
    setError("");
    const id = (picked ?? entityID).trim();
    if (!id) return;
    const seq = ++loadSeq.current;
    const result = await fetchEntity(id).catch(() => null);
    if (seq !== loadSeq.current) return;
    setEntity(result);
    setLoaded(true);
  };

  return (
    <ManageSection>
      <div className="flex flex-wrap items-start gap-3">
        <div className="space-y-1">
          <Label htmlFor="entity-id">{t("fieldLocksMediaLabel")}</Label>
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
              }
            }}
          />
        </div>
        <Button
          type="button"
          variant="outline"
          className="mt-6"
          onClick={() => void onLoad()}
          disabled={!entityID.trim()}
        >
          {t("fieldLocksLoad")}
        </Button>
      </div>

      <div className="mt-4">
        {!loaded ? null : !entity ? (
          <EmptyLine message={t("libraryEmptyTitle")} />
        ) : (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold text-[var(--text-primary)]">{entity.title}</h3>
              <StatusBadge state="neutral">{entity.kind}</StatusBadge>
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
                      <TableCell className="manage-cell-primary">{field.name}</TableCell>
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
