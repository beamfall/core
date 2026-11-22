import type { FixMatchChildInfo } from "@/lib/graphql/manage";
import { useT } from "@/lib/i18n";
import { AssignIndexInput } from "./AssignOrdinalForm";

export type ChildChoice = { key: string; index: string };

export function AssignPairingSelect({
  rows,
  choice,
  title,
  indexLabel,
  disabled,
  onChange,
}: {
  rows: FixMatchChildInfo[];
  choice: ChildChoice;
  title: string;
  indexLabel: string;
  disabled: boolean;
  onChange: (choice: ChildChoice) => void;
}) {
  const t = useT();
  const picked = rows.find(({ key }) => key === choice.key);
  return (
    <div className="grid gap-1">
      <select
        className="min-w-0 rounded-md border bg-card p-2"
        aria-label={t("assignPairItem", { item: title })}
        value={choice.key}
        disabled={disabled}
        onChange={(event) => onChange({ key: event.target.value, index: "" })}
      >
        <option value="">{t("assignSkip")}</option>
        {rows.map((row) => (
          <option key={row.key} value={row.key}>
            {row.index == null ? row.label : `${row.index} · ${row.label}`}
          </option>
        ))}
      </select>
      {picked && picked.index == null && (
        <AssignIndexInput
          label={t("assignMissingIndex", { level: indexLabel })}
          value={choice.index}
          disabled={disabled}
          onChange={(index) => onChange({ ...choice, index })}
        />
      )}
    </div>
  );
}
