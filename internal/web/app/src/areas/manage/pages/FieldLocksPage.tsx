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
import { fetchMediaFieldLocks, setMediaFieldLock } from "@/lib/graphql";
import type { FieldLockInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// fieldLockField_<field> is the localized label for the generic metadata-field
// names the server returns (title / summary / year). Falls back to the raw
// field name for any field key without a localized label.
function fieldLabel(t: (key: StringKey) => string, field: string): string {
  const key = `fieldLockField_${field}` as StringKey;
  const label = t(key);
  return label === key ? field : label;
}

// Media field locks (ADR-0009 §8): per-field source precedence. A locked field
// keeps its value through re-scans and scraper passes. Loaded by Media ID;
// setMediaFieldLock returns the updated row. Ported from app.js loadFieldLocks /
// setFieldLock. The field names are the generic metadata shape the server
// returns — never invented client-side (HARD invariant #1).
export function FieldLocksPage() {
  const t = useT();
  const [mediaID, setMediaID] = useState("");
  const [locks, setLocks] = useState<FieldLockInfo[] | null>(null);
  const [error, setError] = useState("");

  const onLoad = async () => {
    setError("");
    const id = mediaID.trim();
    if (!id) return;
    try {
      setLocks(await fetchMediaFieldLocks(id));
    } catch {
      setError(t("fieldLocksUnavailable"));
    }
  };

  const onToggle = async (lock: FieldLockInfo) => {
    setError("");
    const id = mediaID.trim();
    try {
      const updated = await setMediaFieldLock(id, lock.field, !lock.locked);
      setLocks((prev) => (prev ?? []).map((l) => (l.field === lock.field ? updated : l)));
    } catch {
      setError(t("fieldLocksUnavailable"));
    }
  };

  return (
    <ManageSection title={t("fieldLocksHeading")} lead={t("fieldLocksLead")}>
      <div className="flex items-end gap-3">
        <div className="space-y-1">
          <Label htmlFor="field-locks-media">{t("fieldLocksMediaLabel")}</Label>
          <Input
            id="field-locks-media"
            value={mediaID}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setMediaID(e.target.value)}
            className="w-72"
          />
        </div>
        <Button type="button" variant="outline" onClick={onLoad} disabled={!mediaID.trim()}>
          {t("fieldLocksLoad")}
        </Button>
      </div>

      <div className="mt-3">
        {locks === null ? (
          <EmptyLine message={t("fieldLocksEmpty")} />
        ) : locks.length === 0 ? (
          <EmptyLine message={t("fieldLocksEmpty")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("title")}</TableHead>
                <TableHead>{t("fieldLockLocked")}</TableHead>
                <TableHead className="text-right">{t("fieldLockPin")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {locks.map((lock) => (
                <TableRow key={lock.field}>
                  <TableCell className="manage-cell-primary">{fieldLabel(t, lock.field)}</TableCell>
                  <TableCell>
                    <StatusBadge state={lock.locked ? "green" : "neutral"}>
                      {lock.locked ? t("fieldLockLocked") : t("fieldLockUnlocked")}
                    </StatusBadge>
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => onToggle(lock)}
                    >
                      {lock.locked ? t("fieldLockRelease") : t("fieldLockPin")}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </ManageTable>
        )}
      </div>
      <ErrorLine message={error} />
    </ManageSection>
  );
}
