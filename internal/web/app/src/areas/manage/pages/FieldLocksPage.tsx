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
// setMediaFieldLock returns the Media's whole updated lock list. Ported from app.js loadFieldLocks /
// setFieldLock. The field names are the generic metadata shape the server
// returns — never invented client-side (HARD invariant #1).
export function FieldLocksPage() {
  const t = useT();
  const [mediaID, setMediaID] = useState("");
  // The Media ID the shown locks belong to; toggles write to it.
  const [loadedID, setLoadedID] = useState("");
  const [locks, setLocks] = useState<FieldLockInfo[] | null>(null);
  const [error, setError] = useState("");
  // Only the newest load may land, so a slow earlier pick cannot replace it.
  const loadSeq = useRef(0);

  const onLoad = async (picked?: string) => {
    setError("");
    const id = (picked ?? mediaID).trim();
    if (!id) return;
    const seq = ++loadSeq.current;
    try {
      const next = await fetchMediaFieldLocks(id);
      if (seq !== loadSeq.current) return;
      setLoadedID(id);
      setLocks(next);
    } catch {
      if (seq === loadSeq.current) setError(t("fieldLocksUnavailable"));
    }
  };

  const onClear = () => {
    loadSeq.current++;
    setLoadedID("");
    setLocks(null);
  };

  const onToggle = async (lock: FieldLockInfo) => {
    setError("");
    const id = loadedID;
    if (!id) return;
    // A newer load or Clear since the write started owns the table.
    const seq = loadSeq.current;
    try {
      const next = await setMediaFieldLock(id, lock.field, !lock.locked);
      if (seq === loadSeq.current) setLocks(next);
    } catch {
      if (seq === loadSeq.current) setError(t("fieldLocksUnavailable"));
    }
  };

  return (
    <ManageSection>
      <div className="flex flex-wrap items-start gap-3">
        <div className="space-y-1">
          <Label htmlFor="field-locks-media">{t("fieldLocksMediaLabel")}</Label>
          <EntityPicker
            id="field-locks-media"
            value={mediaID}
            onChange={(id) => {
              setMediaID(id);
              if (id) void onLoad(id);
              else onClear();
            }}
          />
        </div>
        <Button
          type="button"
          variant="outline"
          className="mt-6"
          onClick={() => void onLoad()}
          disabled={!mediaID.trim()}
        >
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
