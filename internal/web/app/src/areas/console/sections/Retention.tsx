import { Plus } from "lucide-react";
import { useState } from "react";
import {
  ControlRow,
  DataTable,
  EmptyState,
  ErrorState,
  PresetSelect,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  createRetentionRule,
  deleteRetentionRule,
  fetchLibraries,
  fetchRetentionRules,
  updateRetentionRule,
} from "@/lib/graphql";
import type { RetentionRuleInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The rule's source: every item in its library (the default) or a smart
// collection query written as JSON, for admins who need one (CORE-2445).
type SourceMode = "all" | "custom";
const ALL_ITEMS_SOURCE = '{"kind":"all-items"}';
const GRACE_PRESETS = [3, 7, 14, 30];
const EMPTY_DRAFT = { name: "", sourceExpr: "", targetLibraryID: "", graceDays: 7, enabled: true };

/** Leaving soon rules (ADR-0051, RET-2): Console UI for retention policy. */
export function Retention() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchRetentionRules);
  const [creating, setCreating] = useState(false);
  const libraries = useAsync(fetchLibraries);
  const [sourceMode, setSourceMode] = useState<SourceMode>("all");
  const [draft, setDraft] = useState(EMPTY_DRAFT);
  const daysLabel = (days: number) => t("sessionLengthDays", { count: days });

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    const sourceExpr = sourceMode === "all" ? ALL_ITEMS_SOURCE : draft.sourceExpr;
    await createRetentionRule({ ...draft, sourceExpr });
    setCreating(false);
    setSourceMode("all");
    setDraft(EMPTY_DRAFT);
    refresh();
  }

  async function handleToggle(rule: RetentionRuleInfo) {
    await updateRetentionRule({ id: rule.id, enabled: !rule.enabled });
    refresh();
  }

  async function handleDelete(rule: RetentionRuleInfo) {
    await deleteRetentionRule(rule.id);
    refresh();
  }

  return (
    <Section
      heading="navLeavingSoonRules"
      lead="retentionPageLead"
      actions={
        <Button size="sm" variant="outline" onClick={() => setCreating((v) => !v)}>
          <Plus className="mr-1 h-4 w-4" />
          {t("retentionRuleAdd")}
        </Button>
      }
    >
      {creating && (
        <form onSubmit={handleCreate} className="console-card console-card-pad mb-4 space-y-3">
          <ControlRow
            label="retentionRuleNameLabel"
            setting="console.retention.name"
            control={
              <Input
                aria-label={t("retentionRuleNameLabel")}
                placeholder={t("retentionRuleNamePlaceholder")}
                value={draft.name}
                onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
                required
              />
            }
          />
          <ControlRow
            label="retentionRuleLibraryLabel"
            tip="retentionRuleLibraryTip"
            setting="console.retention.library"
            control={
              <Select
                value={draft.targetLibraryID}
                onValueChange={(id) => setDraft((d) => ({ ...d, targetLibraryID: id }))}
              >
                <SelectTrigger aria-label={t("retentionRuleLibraryLabel")} className="min-w-48">
                  <SelectValue placeholder={t("retentionRuleLibraryPick")} />
                </SelectTrigger>
                <SelectContent>
                  {(libraries.data ?? []).map((lib) => (
                    <SelectItem key={lib.id} value={lib.id}>
                      {lib.title}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            }
          />
          <ControlRow
            label="retentionRuleSourceLabel"
            tip="retentionRuleSourceTip"
            setting="console.retention.source"
            control={
              <Select
                value={sourceMode}
                onValueChange={(mode) => setSourceMode(mode as SourceMode)}
              >
                <SelectTrigger aria-label={t("retentionRuleSourceLabel")} className="min-w-48">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">{t("retentionRuleSourceAll")}</SelectItem>
                  <SelectItem value="custom">{t("retentionRuleSourceCustom")}</SelectItem>
                </SelectContent>
              </Select>
            }
          />
          {sourceMode === "custom" && (
            <Input
              aria-label={t("retentionRuleSourceCustom")}
              className="console-mono"
              placeholder={t("retentionRuleSourceExprPlaceholder")}
              value={draft.sourceExpr}
              onChange={(e) => setDraft((d) => ({ ...d, sourceExpr: e.target.value }))}
              required
            />
          )}
          <ControlRow
            label="retentionRuleGraceLabel"
            tip="retentionRuleGraceTip"
            setting="console.retention.grace"
            control={
              <PresetSelect
                aria-label={t("retentionRuleGraceLabel")}
                value={draft.graceDays}
                presets={GRACE_PRESETS.map((days) => ({ value: days, label: daysLabel(days) }))}
                format={daysLabel}
                onChange={(graceDays) => setDraft((d) => ({ ...d, graceDays }))}
              />
            }
          />
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={!draft.name.trim() || !draft.targetLibraryID}>
              {t("retentionRuleCreate")}
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setCreating(false)}>
              {t("cancel")}
            </Button>
          </div>
        </form>
      )}
      {loading && <Skeleton className="h-32 w-full" />}
      {failed && <ErrorState message="retentionRulesLoadError" />}
      {!loading && !failed && data && data.length === 0 && (
        <EmptyState message="retentionRulesEmpty" />
      )}
      {!loading && !failed && data && data.length > 0 && (
        <DataTable
          columns={[
            { key: "name" },
            { key: "grace" },
            { key: "enabled" },
            { key: "actions", numeric: true },
          ]}
        >
          {data.map((rule) => (
            <tr key={rule.id}>
              <td className="py-2 pr-4 font-medium">{rule.name}</td>
              <td className="py-2 pr-4 text-sm text-muted-foreground">
                {daysLabel(rule.graceDays)}
              </td>
              <td className="py-2 pr-4">
                <Switch
                  checked={rule.enabled}
                  onCheckedChange={() => handleToggle(rule)}
                  aria-label={t("retentionRuleEnabledToggle")}
                />
              </td>
              <td className="py-2 text-right">
                <ConfirmButton
                  onConfirm={() => handleDelete(rule)}
                  label={t("retentionRuleDelete")}
                  confirm="retentionRuleDeleteConfirm"
                />
              </td>
            </tr>
          ))}
        </DataTable>
      )}
    </Section>
  );
}
