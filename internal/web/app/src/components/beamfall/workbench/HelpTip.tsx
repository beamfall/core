import { CircleHelp } from "lucide-react";
import { Popover } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// HelpTip (CORE-2445): the (?) beside a setting whose name alone does not say
// what it does. A click (or Enter/Space) opens a short explanation; Escape or a
// click outside closes it and focus returns to the button. The button's name
// says which setting it explains, so a screen reader hears "Help: Port".
export function HelpTip({ label, text }: { label: string; text: StringKey }) {
  const t = useT();
  return (
    <Popover.Root>
      <Popover.Trigger
        type="button"
        aria-label={t("settingsHelpFor", { setting: label })}
        className="bf-help-tip"
        data-help-tip=""
      >
        <CircleHelp aria-hidden className="size-4" />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner side="top" align="start" sideOffset={6} className="z-50">
          <Popover.Popup className="bf-help-tip-popup">
            <Popover.Title className="bf-help-tip-title">{label}</Popover.Title>
            <Popover.Description className="bf-help-tip-body">{t(text)}</Popover.Description>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
