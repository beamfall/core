// The house brand backdrop (CORE-1114): one static accent glow falling from
// the top of the cinema base, like light from a projector. Decorative only;
// place it first inside a `relative isolate` container.
export function CinemaBackdrop() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 -z-10 bg-[radial-gradient(70%_50%_at_50%_-10%,color-mix(in_oklab,var(--accent)_16%,transparent),transparent_70%)]"
    />
  );
}
