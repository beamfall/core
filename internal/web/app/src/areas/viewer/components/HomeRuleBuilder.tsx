import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import type { HomeFilterField, SmartRuleGroup, SmartRuleNode } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";

interface HomeRuleBuilderProps {
  group: SmartRuleGroup;
  fields: HomeFilterField[];
  depth?: number;
  onChange: (next: SmartRuleGroup) => void;
  onRemoveGroup?: () => void;
}

const OP_LABELS: Record<string, string> = {
  is: "is",
  isNot: "is not",
  contains: "contains",
  notContains: "does not contain",
  beginsWith: "begins with",
  in: "is any of",
  notIn: "is none of",
  gt: ">",
  gte: "≥",
  lt: "<",
  lte: "≤",
  between: "between",
  inLast: "in last (days)",
  notInLast: "not in last (days)",
  before: "before",
  after: "after",
  isTrue: "is true",
  isFalse: "is false",
};

const GROUP_LABELS: Record<HomeFilterField["group"], string> = {
  general: "General",
  activity: "My Activity",
  metadata: "Metadata",
  entities: "People & Tags",
};

export function HomeRuleBuilder({
  group,
  fields,
  depth = 0,
  onChange,
  onRemoveGroup,
}: HomeRuleBuilderProps) {
  const t = useT();
  const locale = useDisplayLocale();
  const defaultField = fields[0];

  function updateCondition(index: number, nextNode: SmartRuleNode) {
    const next = [...group.conditions];
    next[index] = nextNode;
    onChange({ ...group, conditions: next });
  }

  function removeCondition(index: number) {
    onChange({
      ...group,
      conditions: group.conditions.filter((_, i) => i !== index),
    });
  }

  function addLeafRule() {
    if (!defaultField) return;
    const defaultOp = defaultField.operators[0] ?? "is";
    const defaultValue = defaultField.valueType === "number" ? 0 : "";
    onChange({
      ...group,
      conditions: [
        ...group.conditions,
        { field: defaultField.key, op: defaultOp, value: defaultValue },
      ],
    });
  }

  function addNestedGroup() {
    if (depth >= 1 || !defaultField) return;
    onChange({
      ...group,
      conditions: [
        ...group.conditions,
        {
          group: {
            match: "any",
            conditions: [
              {
                field: defaultField.key,
                op: defaultField.operators[0] ?? "is",
                value: defaultField.valueType === "number" ? 0 : "",
              },
            ],
          },
        },
      ],
    });
  }

  const groupedFields = (["general", "activity", "metadata", "entities"] as const)
    .map((g) => ({ group: g, items: fields.filter((f) => f.group === g) }))
    .filter((g) => g.items.length > 0);

  return (
    <div
      className={
        "flex flex-col gap-2 rounded-md border border-[var(--border)] p-3" +
        (depth > 0 ? " bg-muted/20" : "")
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <div className="inline-flex rounded-md border border-[var(--border)] p-0.5 text-xs">
          {(["all", "any", "none"] as const).map((mode) => (
            <button
              key={mode}
              type="button"
              onClick={() => onChange({ ...group, match: mode })}
              className={
                "rounded px-2 py-1 font-medium transition-colors " +
                (group.match === mode
                  ? "bg-primary text-primary-foreground"
                  : "text-[var(--text-secondary)] hover:text-foreground")
              }
            >
              {mode === "all"
                ? t("homeShelfMatchAll")
                : mode === "any"
                  ? t("homeShelfMatchAny")
                  : t("homeShelfMatchNone")}
            </button>
          ))}
        </div>

        <div className="ml-auto flex items-center gap-1.5">
          <Button type="button" variant="outline" size="sm" onClick={addLeafRule}>
            <Plus className="mr-1 size-3.5" aria-hidden />
            {t("homeShelfAddRule")}
          </Button>
          {depth === 0 && (
            <Button type="button" variant="outline" size="sm" onClick={addNestedGroup}>
              <Plus className="mr-1 size-3.5" aria-hidden />
              {t("homeShelfAddGroup")}
            </Button>
          )}
          {onRemoveGroup && (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="Remove group"
              onClick={onRemoveGroup}
            >
              <Trash2 className="size-3.5" aria-hidden />
            </Button>
          )}
        </div>
      </div>

      {group.conditions.length === 0 ? (
        <p className="text-xs text-[var(--text-secondary)]">
          No rules yet — matches all items in the selected libraries.
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          {group.conditions.map((node, index) => {
            if (node.group) {
              return (
                <HomeRuleBuilder
                  key={`g-${index}`}
                  group={node.group}
                  fields={fields}
                  depth={depth + 1}
                  onChange={(sub) => updateCondition(index, { group: sub })}
                  onRemoveGroup={() => removeCondition(index)}
                />
              );
            }

            const fieldSpec = fields.find((f) => f.key === node.field) ?? defaultField;
            const ops = fieldSpec?.operators ?? ["is"];
            const currentOp = ops.includes(node.op ?? "") ? (node.op as string) : ops[0];
            const isBoolOp = currentOp === "isTrue" || currentOp === "isFalse";
            const isListOp = currentOp === "in" || currentOp === "notIn";
            const isBetweenOp = currentOp === "between";
            const isRelativeDate = currentOp === "inLast" || currentOp === "notInLast";

            return (
              <div
                key={`r-${index}`}
                className="flex flex-wrap items-center gap-2 rounded border border-[var(--border)]/60 bg-background/40 p-2"
              >
                <select
                  aria-label="Rule field"
                  value={node.field ?? fieldSpec?.key ?? ""}
                  onChange={(e) => {
                    const nextField = fields.find((f) => f.key === e.target.value);
                    const nextOp = nextField?.operators[0] ?? "is";
                    updateCondition(index, {
                      field: e.target.value,
                      op: nextOp,
                      value: nextField?.valueType === "number" ? 0 : "",
                    });
                  }}
                  className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
                >
                  {groupedFields.map((grp) => (
                    <optgroup key={grp.group} label={GROUP_LABELS[grp.group]}>
                      {grp.items.map((f) => (
                        <option key={f.key} value={f.key}>
                          {displayString(f.label, locale) || f.key}
                        </option>
                      ))}
                    </optgroup>
                  ))}
                </select>

                <select
                  aria-label="Rule operator"
                  value={currentOp}
                  onChange={(e) => {
                    const nextOp = e.target.value;
                    let nextVal: unknown = node.value;
                    if (nextOp === "in" || nextOp === "notIn") {
                      nextVal = Array.isArray(node.value)
                        ? node.value
                        : typeof node.value === "string" && node.value
                          ? [node.value]
                          : [];
                    } else if (nextOp === "between") {
                      nextVal = [0, 10];
                    } else if (nextOp === "inLast" || nextOp === "notInLast") {
                      nextVal = 30;
                    } else if (nextOp === "isTrue" || nextOp === "isFalse") {
                      nextVal = undefined;
                    }
                    updateCondition(index, { field: node.field, op: nextOp, value: nextVal });
                  }}
                  className="h-8 rounded border border-[var(--border)] bg-background px-2 text-xs"
                >
                  {ops.map((op) => (
                    <option key={op} value={op}>
                      {OP_LABELS[op] ?? op}
                    </option>
                  ))}
                </select>

                {!isBoolOp && isBetweenOp && (
                  <div className="flex items-center gap-1">
                    <input
                      type="number"
                      aria-label="Minimum value"
                      value={Array.isArray(node.value) ? Number(node.value[0] ?? 0) : 0}
                      onChange={(e) => {
                        const arr = Array.isArray(node.value) ? [...node.value] : [0, 10];
                        arr[0] = Number(e.target.value);
                        updateCondition(index, { ...node, value: arr });
                      }}
                      className="h-8 w-20 rounded border border-[var(--border)] bg-background px-2 text-xs"
                    />
                    <span className="text-xs text-[var(--text-secondary)]">–</span>
                    <input
                      type="number"
                      aria-label="Maximum value"
                      value={Array.isArray(node.value) ? Number(node.value[1] ?? 10) : 10}
                      onChange={(e) => {
                        const arr = Array.isArray(node.value) ? [...node.value] : [0, 10];
                        arr[1] = Number(e.target.value);
                        updateCondition(index, { ...node, value: arr });
                      }}
                      className="h-8 w-20 rounded border border-[var(--border)] bg-background px-2 text-xs"
                    />
                  </div>
                )}

                {!isBoolOp && isListOp && (
                  <div className="flex min-w-44 flex-1 flex-col gap-1">
                    <input
                      type="text"
                      aria-label="Comma-separated values"
                      placeholder="Sci-Fi, Noir, Thriller"
                      value={
                        Array.isArray(node.value) ? node.value.join(", ") : String(node.value ?? "")
                      }
                      onChange={(e) => {
                        const list = e.target.value
                          .split(",")
                          .map((s) => s.trim())
                          .filter(Boolean);
                        updateCondition(index, { ...node, value: list });
                      }}
                      className="h-8 w-full rounded border border-[var(--border)] bg-background px-2 text-xs"
                    />
                    {fieldSpec?.suggestions && fieldSpec.suggestions.length > 0 && (
                      <div className="flex flex-wrap gap-1">
                        {fieldSpec.suggestions.slice(0, 12).map((s) => {
                          const current = Array.isArray(node.value) ? node.value.map(String) : [];
                          const active = current.some((v) => v.toLowerCase() === s.toLowerCase());
                          return (
                            <button
                              key={s}
                              type="button"
                              onClick={() => {
                                const next = active
                                  ? current.filter((v) => v.toLowerCase() !== s.toLowerCase())
                                  : [...current, s];
                                updateCondition(index, { ...node, value: next });
                              }}
                              className={
                                "rounded-full border px-2 py-0.5 text-[10px] transition-colors " +
                                (active
                                  ? "border-primary bg-primary/15 text-primary"
                                  : "border-[var(--border)] text-[var(--text-secondary)] hover:text-foreground")
                              }
                            >
                              {s}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </div>
                )}

                {!isBoolOp && isRelativeDate && (
                  <div className="flex items-center gap-1">
                    <input
                      type="number"
                      min={1}
                      max={36500}
                      aria-label="Number of days"
                      value={Number(node.value ?? 30)}
                      onChange={(e) =>
                        updateCondition(index, {
                          ...node,
                          value: Math.max(1, Number(e.target.value)),
                        })
                      }
                      className="h-8 w-20 rounded border border-[var(--border)] bg-background px-2 text-xs"
                    />
                    {[7, 30, 90, 365].map((days) => (
                      <button
                        key={days}
                        type="button"
                        onClick={() => updateCondition(index, { ...node, value: days })}
                        className="rounded border border-[var(--border)] px-1.5 py-0.5 text-[11px] hover:bg-muted"
                      >
                        {days}d
                      </button>
                    ))}
                  </div>
                )}

                {!isBoolOp && !isBetweenOp && !isListOp && !isRelativeDate && (
                  <>
                    <input
                      type={
                        fieldSpec?.valueType === "number"
                          ? "number"
                          : fieldSpec?.valueType === "date"
                            ? "date"
                            : "text"
                      }
                      list={
                        fieldSpec?.suggestions?.length ? `rule-sugg-${depth}-${index}` : undefined
                      }
                      aria-label="Rule value"
                      value={String(node.value ?? "")}
                      onChange={(e) => {
                        const v =
                          fieldSpec?.valueType === "number"
                            ? Number(e.target.value)
                            : e.target.value;
                        updateCondition(index, { ...node, value: v });
                      }}
                      className="h-8 min-w-36 flex-1 rounded border border-[var(--border)] bg-background px-2 text-xs"
                    />
                    {fieldSpec?.suggestions && fieldSpec.suggestions.length > 0 && (
                      <datalist id={`rule-sugg-${depth}-${index}`}>
                        {fieldSpec.suggestions.map((s) => (
                          <option key={s} value={s} />
                        ))}
                      </datalist>
                    )}
                  </>
                )}

                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Remove rule"
                  onClick={() => removeCondition(index)}
                >
                  <Trash2 className="size-3.5" aria-hidden />
                </Button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
