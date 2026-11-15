import { useMemo } from "react";
import { encodeQR } from "@/lib/qr/encode";

const QUIET_ZONE = 4;

/**
 * A QR code for `value`, drawn as one SVG path (CORE-1614). It is always dark
 * modules on a light square, whatever the theme: phone cameras read inverted
 * codes unreliably. `label` names it for assistive technology; show the value
 * as text next to it as well, since not everyone can scan.
 */
export function QrCode({
  value,
  label,
  className,
}: {
  value: string;
  label: string;
  className?: string;
}) {
  const { side, path } = useMemo(() => {
    const { matrix } = encodeQR(value);
    let d = "";
    matrix.forEach((row, y) => {
      row.forEach((dark, x) => {
        if (dark) d += `M${x + QUIET_ZONE} ${y + QUIET_ZONE}h1v1h-1z`;
      });
    });
    return { side: matrix.length + QUIET_ZONE * 2, path: d };
  }, [value]);
  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${side} ${side}`}
      shapeRendering="crispEdges"
      className={className}
      data-qr-value={value}
    >
      <rect width={side} height={side} fill="white" />
      <path d={path} fill="black" />
    </svg>
  );
}
