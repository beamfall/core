import { Clock, Plus } from "lucide-react";
import { useState } from "react";
import { DataTable, EmptyState, ErrorState } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  createRetentionRule,
  deleteRetentionRule,
  fetchRetentionRules,
  updateRetentionRule,
} from "@/lib/graphql";
import type { RetentionRuleInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

/** Retention rules (ADR-0051, RET-2): Console UI for leaving-soon policy. */
export function Retention() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchRetentionRules);
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState({
    name: "",
    sourceExpr: "",
    targetLibraryID: "",
    graceDays: 7,
    enabled: true,
  });

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    await createRetentionRule({ ...draft });
    setCreating(false);
    setDraft({ name: "", sourceExpr: "", targetLibraryID: "", graceDays: 7, enabled: true });
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
      heading="retentionRulesTitle"
      actions={
        <Button size="sm" variant="outline" onClick={() => setCreating((v) => !v)}>
          <Plus className="mr-1 h-4 w-4" />
          {t("retentionRuleAdd")}
        </Button>
      }
    >
      {creating && (
        <form onSubmit={handleCreate} className="mb-4 flex flex-col gap-2 rounded-md border p-4">
          <Input
            placeholder={t("retentionRuleNamePlaceholder")}
            value={draft.name}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
            required
          />
          <Input
            placeholder={t("retentionRuleSourceExprPlaceholder")}
            value={draft.sourceExpr}
            onChange={(e) => setDraft((d) => ({ ...d, sourceExpr: e.target.value }))}
            required
          />
          <Input
            placeholder={t("retentionRuleTargetLibraryIDPlaceholder")}
            value={draft.targetLibraryID}
            onChange={(e) => setDraft((d) => ({ ...d, targetLibraryID: e.target.value }))}
            required
          />
          <Input
            type="number"
            min={1}
            value={draft.graceDays}
            onChange={(e) => setDraft((d) => ({ ...d, graceDays: Number(e.target.value) }))}
          />
          <div className="flex gap-2">
            <Button type="submit" size="sm">
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
        <EmptyState message="retentionRulesEmpty" icon={Clock} />
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
              <td className="py-2 pr-4 text-sm text-muted-foreground">{rule.graceDays}d</td>
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
