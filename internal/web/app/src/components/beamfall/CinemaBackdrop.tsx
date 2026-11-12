// The house brand backdrop (CORE-1114): layered accent glows and a faint grid
// over the cinema base. Static and decorative; place it first inside a
// `relative isolate` container.
export function CinemaBackdrop() {
  return (
    <>
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 -z-10 bg-[radial-gradient(55%_55%_at_10%_-5%,color-mix(in_oklab,var(--accent)_42%,transparent),transparent_70%),radial-gradient(50%_45%_at_100%_105%,color-mix(in_oklab,var(--accent)_30%,transparent),transparent_75%),radial-gradient(40%_35%_at_85%_5%,rgba(139,92,246,0.28),transparent_70%),radial-gradient(30%_30%_at_5%_100%,rgba(236,72,153,0.12),transparent_70%)]"
      />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 -z-10 opacity-[0.07] [background-image:linear-gradient(to_right,currentColor_1px,transparent_1px),linear-gradient(to_bottom,currentColor_1px,transparent_1px)] [background-size:56px_56px] [mask-image:radial-gradient(ellipse_at_center,black,transparent_75%)]"
      />
    </>
  );
}
