// Player Profiles are listed by both the Profiles and Devices sections of the
// console. Profiles announces a change here so Devices reloads its binding
// choices without a page reload (ADR-0240 D2).
const listeners = new Set<() => void>();

export function announcePlayerProfilesChanged(): void {
  for (const listener of listeners) listener();
}

export function onPlayerProfilesChanged(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
