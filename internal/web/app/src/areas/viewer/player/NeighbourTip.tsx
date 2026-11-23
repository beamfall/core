// The hover/focus label over Previous and Next (CORE-3216). The control's own
// accessible name already carries the title, so this is visual only.
export function NeighbourTip({ eyebrow, title }: { eyebrow: string; title: string }) {
  return (
    <span className="bf-neighbour-tip" aria-hidden="true">
      <span className="bf-neighbour-tip-eyebrow">{eyebrow}</span>
      <span className="bf-neighbour-tip-title">{title}</span>
    </span>
  );
}
