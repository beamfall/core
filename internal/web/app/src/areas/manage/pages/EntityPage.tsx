import { useState } from "react";
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

  const onLoad = async () => {
    setError("");
    const id = entityID.trim();
    if (!id) return;
    try {
      const result = await fetchEntity(id);
      setEntity(result);
      setLoaded(true);
    } catch {
      setEntity(null);
      setLoaded(true);
    }
  };

  return (
    <ManageSection title={t("manageTitle")}>
      <div className="flex items-end gap-3">
        <div className="space-y-1">
          <Label htmlFor="entity-id">{t("fieldLocksMediaLabel")}</Label>
          <Input
            id="entity-id"
            value={entityID}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setEntityID(e.target.value)}
            className="w-72"
          />
        </div>
        <Button type="button" variant="outline" onClick={onLoad} disabled={!entityID.trim()}>
          {t("fieldLocksLoad")}
        </Button>
      </div>

      <div className="mt-4">
        {!loaded ? null : !entity ? (
          <EmptyLine message={t("libraryEmptyTitle")} />
        ) : (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-bold text-[var(--text-primary)]">{entity.title}</h3>
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
