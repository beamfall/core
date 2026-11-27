import type { ReactElement, ReactNode } from "react";
import { Tooltip } from "@/components/ui/astryx-primitives";

/**
 * Shared hover-warmup provider for adjacent icon-only action buttons so
 * moving between neighbouring controls switches tooltips immediately.
 */
export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <Tooltip.Provider delay={250} closeDelay={80}>
      {children}
    </Tooltip.Provider>
  );
}

/**
 * Shows a concise visual tooltip on hover and keyboard focus for icon-only
 * controls whose accessible name is already carried by `aria-label`.
 *
 * The popup is marked `aria-hidden="true"` so screen readers do not announce
 * the button's label twice.
 */
export function ActionTooltip({
  label,
  side = "top",
  disabled = false,
  children,
}: {
  label: string;
  side?: "top" | "bottom" | "left" | "right";
  disabled?: boolean;
  children: ReactElement;
}) {
  if (!label) return children;
  return (
    <Tooltip.Root disabled={disabled}>
      <Tooltip.Trigger render={children} />
      <Tooltip.Portal>
        <Tooltip.Positioner side={side} sideOffset={6} className="bf-tooltip-layer">
          <Tooltip.Popup className="bf-tooltip" aria-hidden="true">
            {label}
          </Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}
