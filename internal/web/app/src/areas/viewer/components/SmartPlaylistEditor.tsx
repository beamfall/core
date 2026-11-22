import { Plus, X } from "lucide-react";
import { type FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";
import {
  newSmartRule,
  SMART_BUILTIN_FIELDS,
  SMART_RULE_MAX_LIMIT,
  SMART_RULE_MAX_RULES,
  SMART_SORT_KEYS,
  type SmartMatch,
  type SmartPlaylist,
  type SmartRule,
  type SmartSortDir,
  saveSmartPlaylist,
  smartFieldOptions,
  smartOperators,
  smartRulesComplete,
  smartValueKind,
  withSmartField,
} from "../lib/smartPlaylists";

const SELECT_CLASS =
  "h-10 rounded-[var(--radius-control)] border border-input bg-transparent px-3 text-sm";

const FIELD_LABELS: Record<string, StringKey> = {
  title: "smartFieldTitle",
  duration: "smartFieldDuration",
  year: "smartFieldYear",
  playCount: "smartFieldPlayCount",
  rating: "smartFieldRating",
  lastPlayed: "smartFieldLastPlayed",
  added: "smartSortAdded",
  random: "smartSortRandom",
};

const OP_LABELS: Record<string, StringKey> = {
  is: "smartOpIs",
  isNot: "smartOpIsNot",
  contains: "smartOpContains",
  notContains: "smartOpNotContains",
  beginsWith: "smartOpBeginsWith",
  gt: "smartOpGt",
  gte: "smartOpGte",
  lt: "smartOpLt",
  lte: "smartOpLte",
  inLast: "smartOpInLast",
  notInLast: "smartOpNotInLast",
  before: "smartOpBefore",
  after: "smartOpAfter",
  isTrue: "smartOpIsTrue",
  isFalse: "smartOpIsFalse",
};

// The "Other field…" choice: a plugin Media field the manifest does not name.
const OTHER_FIELD = "field:";

// Create or edit one smart playlist (CORE-2771): a name, its Library, match
// all/any, up to 20 rules over generic Media fields, a sort and a limit. The
// server validates the rules again and evaluates them on every read.
export function SmartPlaylistEditor({
  playlist,
  onClose,
  onSaved,
}: {
  /** The playlist being edited; omitted to create one. */
  playlist?: SmartPlaylist;
  onClose: () => void;
  onSaved: (playlist: SmartPlaylist | null) => void;
}) {
  const t = useT();
  const { manifest } = useSession();
  const libraries = manifest?.libraries ?? [];
  const { entityKinds, pluginFields } = smartFieldOptions(manifest);
  const [name, setName] = useState(playlist?.name ?? "");
  const [libraryID, setLibraryID] = useState(playlist?.libraryID ?? libraries[0]?.id ?? "");
  const [match, setMatch] = useState<SmartMatch>(playlist?.rules.match ?? "all");
  const [rules, setRules] = useState<SmartRule[]>(playlist?.rules.rules ?? [newSmartRule()]);
  const [sortBy, setSortBy] = useState(playlist?.sort.by ?? "title");
  const [sortDir, setSortDir] = useState<SmartSortDir>(playlist?.sort.dir ?? "asc");
  const [limit, setLimit] = useState(playlist?.rules.limit ? String(playlist.rules.limit) : "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const limitValue = limit.trim() ? Number(limit) : null;
  const ruleSet = { match, rules, limit: limitValue };
  const complete = Boolean(name.trim()) && Boolean(libraryID) && smartRulesComplete(ruleSet);
  const pluginFieldNames = new Set(pluginFields.map((field) => `field:${field}`));

  function fieldLabel(field: string): string {
    const key = FIELD_LABELS[field];
    if (key) return t(key);
    return field.slice(field.indexOf(":") + 1);
  }

  function update(index: number, next: SmartRule) {
    setRules((current) => current.map((rule, i) => (i === index ? next : rule)));
  }

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    if (!complete) {
      setError(t("smartPlaylistIncomplete"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const saved = await saveSmartPlaylist(
        { libraryID, name, rules: ruleSet, sort: { by: sortBy, dir: sortDir } },
        playlist?.id,
      );
      onSaved(saved);
    } catch {
      setError(t("playlistActionFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>
              {playlist ? t("smartPlaylistEditTitle") : t("smartPlaylistNew")}
            </DialogTitle>
            <DialogDescription>{t("smartPlaylistLead")}</DialogDescription>
          </DialogHeader>
          <div className="bf-list-toolbar">
            <Input
              aria-label={t("playlistNameLabel")}
              placeholder={t("playlistNameLabel")}
              value={name}
              onChange={(event) => setName(event.target.value)}
              className="max-w-xs"
            />
            {!playlist && libraries.length > 1 && (
              <select
                aria-label={t("playlistLibraryLabel")}
                className={SELECT_CLASS}
                value={libraryID}
                onChange={(event) => setLibraryID(event.target.value)}
              >
                {libraries.map((library) => (
                  <option key={library.id} value={library.id}>
                    {library.title}
                  </option>
                ))}
              </select>
            )}
          </div>
          <label className="bf-list-toolbar text-sm">
            {t("smartPlaylistMatchLabel")}
            <select
              className={SELECT_CLASS}
              value={match}
              onChange={(event) => setMatch(event.target.value === "any" ? "any" : "all")}
            >
              <option value="all">{t("smartPlaylistMatchAll")}</option>
              <option value="any">{t("smartPlaylistMatchAny")}</option>
            </select>
          </label>
          {rules.length === 0 && (
            <p className="text-sm text-muted-foreground">{t("smartPlaylistNoRules")}</p>
          )}
          <ol className="m-0 flex list-none flex-col gap-2 p-0">
            {rules.map((rule, index) => {
              const number = index + 1;
              const custom = rule.field.startsWith("field:") && !pluginFieldNames.has(rule.field);
              const valueKind = smartValueKind(rule.field, rule.op);
              return (
                <li key={index} className="bf-list-toolbar">
                  <select
                    aria-label={t("smartPlaylistRuleField", { number })}
                    className={SELECT_CLASS}
                    value={custom ? OTHER_FIELD : rule.field}
                    onChange={(event) => update(index, withSmartField(rule, event.target.value))}
                  >
                    {SMART_BUILTIN_FIELDS.map((field) => (
                      <option key={field} value={field}>
                        {fieldLabel(field)}
                      </option>
                    ))}
                    {entityKinds.length > 0 && (
                      <optgroup label={t("smartFieldGroupLinked")}>
                        {entityKinds.map((kind) => (
                          <option key={kind} value={`entity:${kind}`}>
                            {kind}
                          </option>
                        ))}
                      </optgroup>
                    )}
                    <optgroup label={t("smartFieldGroupFields")}>
                      {pluginFields.map((field) => (
                        <option key={field} value={`field:${field}`}>
                          {field}
                        </option>
                      ))}
                      <option value={OTHER_FIELD}>{t("smartFieldOther")}</option>
                    </optgroup>
                  </select>
                  {custom && (
                    <Input
                      aria-label={t("smartPlaylistRuleFieldName", { number })}
                      value={rule.field.slice(OTHER_FIELD.length)}
                      onChange={(event) =>
                        update(index, { ...rule, field: `${OTHER_FIELD}${event.target.value}` })
                      }
                      className="max-w-40"
                    />
                  )}
                  <select
                    aria-label={t("smartPlaylistRuleOp", { number })}
                    className={SELECT_CLASS}
                    value={rule.op}
                    onChange={(event) => update(index, { ...rule, op: event.target.value })}
                  >
                    {(smartOperators(rule.field).length
                      ? smartOperators(rule.field)
                      : smartOperators("field:x")
                    ).map((op) => (
                      <option key={op} value={op}>
                        {t(OP_LABELS[op])}
                      </option>
                    ))}
                  </select>
                  {valueKind !== "none" && (
                    <Input
                      aria-label={t("smartPlaylistRuleValue", { number })}
                      type={
                        valueKind === "date" ? "date" : valueKind === "text" ? "text" : "number"
                      }
                      min={valueKind === "days" ? 1 : undefined}
                      step={valueKind === "number" ? "any" : undefined}
                      value={rule.value}
                      onChange={(event) => update(index, { ...rule, value: event.target.value })}
                      className="max-w-48"
                    />
                  )}
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("smartPlaylistRemoveRule", { number })}
                    onClick={() => setRules((current) => current.filter((_, i) => i !== index))}
                  >
                    <X aria-hidden size={16} />
                  </Button>
                </li>
              );
            })}
          </ol>
          <Button
            type="button"
            variant="outline"
            className="self-start"
            disabled={rules.length >= SMART_RULE_MAX_RULES}
            onClick={() => setRules((current) => [...current, newSmartRule()])}
          >
            <Plus aria-hidden size={16} />
            {t("smartPlaylistAddRule")}
          </Button>
          <div className="bf-list-toolbar text-sm">
            <span aria-hidden>{t("smartPlaylistSortLabel")}</span>
            <select
              aria-label={t("smartPlaylistSortLabel")}
              className={SELECT_CLASS}
              value={sortBy}
              onChange={(event) => setSortBy(event.target.value)}
            >
              {SMART_SORT_KEYS.map((key) => (
                <option key={key} value={key}>
                  {fieldLabel(key)}
                </option>
              ))}
              {pluginFields.map((field) => (
                <option key={field} value={`field:${field}`}>
                  {field}
                </option>
              ))}
              {sortBy.startsWith("field:") && !pluginFieldNames.has(sortBy) && (
                <option value={sortBy}>{fieldLabel(sortBy)}</option>
              )}
            </select>
            <select
              aria-label={t("smartPlaylistSortDir")}
              className={SELECT_CLASS}
              value={sortDir}
              disabled={sortBy === "random"}
              onChange={(event) => setSortDir(event.target.value === "desc" ? "desc" : "asc")}
            >
              <option value="asc">{t("smartPlaylistSortAsc")}</option>
              <option value="desc">{t("smartPlaylistSortDesc")}</option>
            </select>
            <span aria-hidden>{t("smartPlaylistLimitLabel")}</span>
            <Input
              aria-label={t("smartPlaylistLimitLabel")}
              placeholder={t("smartPlaylistLimitNone")}
              type="number"
              min={1}
              max={SMART_RULE_MAX_LIMIT}
              value={limit}
              onChange={(event) => setLimit(event.target.value)}
              className="max-w-36"
            />
          </div>
          {error && (
            <p role="alert" className="text-sm text-[var(--state-danger)]">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t("playlistCancel")}
            </Button>
            <Button type="submit" disabled={busy}>
              {playlist ? t("playlistSave") : t("playlistCreate")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
