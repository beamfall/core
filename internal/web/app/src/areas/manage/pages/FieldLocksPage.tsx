import { useRef, useState } from "react";
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
import { fetchMediaFieldLocks, fetchMediaMetadata, setMediaFieldLock } from "@/lib/graphql";
import type { FieldLockInfo, MediaMetadataInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// fieldLockField_<field> is the localized label for the generic metadata-field
// names the server returns (title / summary / year). Falls back to a spaced
// label for any field key without a localized label.
function fieldLabel(t: (key: StringKey) => string, field: string): string {
  const key = `fieldLockField_${field}` as StringKey;
  const label = t(key);
  return label === key ? field.replace(/([a-z])([A-Z])/g, "$1 $2") : label;
}

function fieldValue(meta: MediaMetadataInfo | null, field: string): string {
  if (!meta) return "";
  if (field === "title") return meta.title || "";
  if (field === "sortTitle") return meta.sortTitle || "";
  if (field === "year") return meta.year > 0 ? String(meta.year) : "";
  if (field === "summary") return meta.summary || "";
  return "";
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
  const [meta, setMeta] = useState<MediaMetadataInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [busyField, setBusyField] = useState("");
  const [error, setError] = useState("");
  // Only the newest load may land, so a slow earlier pick cannot replace it.
  const loadSeq = useRef(0);

  const onLoad = async (picked?: string) => {
    setError("");
    const id = (picked ?? mediaID).trim();
    if (!id) return;
    const seq = ++loadSeq.current;
    // Hide the old table while loading so no toggle can write to it mid-load.
    setLoadedID("");
    setLocks(null);
    setMeta(null);
    setLoading(true);
    try {
      const [next, nextMeta] = await Promise.all([
        fetchMediaFieldLocks(id),
        typeof fetchMediaMetadata === "function"
          ? fetchMediaMetadata(id).catch(() => null)
          : Promise.resolve(null),
      ]);
      if (seq !== loadSeq.current) return;
      setLoadedID(id);
      setLocks(next);
      setMeta(nextMeta);
    } catch {
      if (seq === loadSeq.current) setError(t("fieldLocksUnavailable"));
    } finally {
      if (seq === loadSeq.current) setLoading(false);
    }
  };

  const onClear = () => {
    loadSeq.current++;
    setLoadedID("");
    setLocks(null);
    setMeta(null);
    setLoading(false);
    setError("");
  };

  const onToggle = async (lock: FieldLockInfo) => {
    setError("");
    const id = loadedID;
    if (!id || busyField) return;
    // A load or Clear started since the write owns the table.
    const seq = loadSeq.current;
    setBusyField(lock.field);
    try {
      const next = await setMediaFieldLock(id, lock.field, !lock.locked);
      if (seq === loadSeq.current) setLocks(next);
    } catch {
      if (seq === loadSeq.current) setError(t("fieldLocksUnavailable"));
    } finally {
      setBusyField("");
    }
  };

  return (
    <ManageSection>
      <div className="flex flex-wrap items-end justify-between gap-3">
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
        {loadedID && (
          <Button asChild variant="outline" size="sm">
            <Link to={`/manage/items/${encodeURIComponent(loadedID)}`}>
              {t("itemMetadataHeading")}
            </Link>
          </Button>
        )}
      </div>

      <div className="mt-3">
        {loading ? (
          <ManageTable>
            <TableBody>
              <TableSkeletonRows columns={4} rows={4} />
            </TableBody>
          </ManageTable>
        ) : !loadedID || locks === null || locks.length === 0 ? (
          <EmptyLine message={t("fieldLocksEmpty")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("bulkEditFieldName")}</TableHead>
                <TableHead>{t("bulkEditFieldValue")}</TableHead>
                <TableHead>{t("consoleOverviewStatus")}</TableHead>
                <TableHead className="text-right">{t("tagActionsHeading")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {locks.map((lock) => {
                const value = fieldValue(meta, lock.field);
                return (
                  <TableRow key={lock.field}>
                    <TableCell className="manage-cell-primary">
                      {fieldLabel(t, lock.field)}
                    </TableCell>
                    <TableCell className="max-w-xs break-words text-muted-foreground" title={value}>
                      {value || "—"}
                    </TableCell>
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
                        disabled={busyField !== ""}
                        onClick={() => void onToggle(lock)}
                      >
                        {busyField === lock.field
                          ? t("loading")
                          : lock.locked
                            ? t("fieldLockRelease")
                            : t("fieldLockPin")}
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </ManageTable>
        )}
      </div>
      <ErrorLine message={error} />
    </ManageSection>
  );
}
