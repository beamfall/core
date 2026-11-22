import { Button } from "@/components/ui/button";
import type { FixMatchChildInfo } from "@/lib/graphql/manage";
import { useT } from "@/lib/i18n";

export type ChildColumn = {
  parent?: FixMatchChildInfo;
  rows?: FixMatchChildInfo[];
  failed?: boolean;
};

export function AssignChildColumn({
  label,
  column,
  selectedKey,
  disabled,
  onPick,
  onRetry,
}: {
  label: string;
  column?: ChildColumn;
  selectedKey?: string;
  disabled: boolean;
  onPick?: (row: FixMatchChildInfo) => void;
  onRetry: () => void;
}) {
  const t = useT();
  return (
    <>
      <h4 className="text-sm font-semibold">{label}</h4>
      {!column ? (
        <p className="text-xs text-muted-foreground">{t("assignChooseParent")}</p>
      ) : column.failed ? (
        <div className="space-y-1">
          <p role="alert" className="text-sm">
            {t("assignChildrenFailed")}
          </p>
          <Button size="sm" variant="outline" disabled={disabled} onClick={onRetry}>
            {t("tryAgain")}
          </Button>
        </div>
      ) : !column.rows ? (
        <p role="status" className="text-sm text-muted-foreground">
          {t("loading")}
        </p>
      ) : column.rows.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t("assignNoChildren")}</p>
      ) : (
        <ul className="space-y-2">
          {column.rows.map((row) => {
            const content = (
              <>
                {row.artworkURL && (
                  <img
                    className="h-10 w-8 shrink-0 rounded object-cover"
                    src={row.artworkURL}
                    alt={row.label}
                    loading="lazy"
                    referrerPolicy="no-referrer"
                  />
                )}
                <span className="min-w-0 break-words text-sm font-medium">
                  <span aria-hidden={row.artworkURL ? true : undefined}>{row.label}</span>
                  {row.year ? ` (${row.year})` : ""}
                </span>
              </>
            );
            return (
              <li key={row.key}>
                {onPick ? (
                  <button
                    type="button"
                    className="flex w-full items-start gap-2 rounded-md border px-3 py-2 text-left hover:bg-muted/50 aria-pressed:border-primary aria-pressed:bg-muted/40"
                    aria-pressed={selectedKey === row.key}
                    disabled={disabled}
                    onClick={() => onPick(row)}
                  >
                    {content}
                  </button>
                ) : (
                  <div className="flex items-start gap-2 rounded-md border px-3 py-2">
                    {content}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </>
  );
}
