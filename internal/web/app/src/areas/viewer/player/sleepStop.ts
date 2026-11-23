// The sleep timer's stop, as loads see it (CORE-3365): a load under way when
// the timer stops playback must not start itself afterwards.

// Bumped each time the sleep timer stops playback.
let stops = 0;
// The count when a skip last moved on, until the load it asked for takes it,
// however long that takes: the timer can stop between the skip and that load
// beginning. Playback starting lets it go, so a skip that never loads holds
// nothing back.
let skip: number | null = null;

/** Records that the sleep timer stopped playback. */
export function noteSleepStop(): void {
  stops += 1;
}

/** A mark to pass to `sleepHoldsAutoplay` later: taken when playback starts. */
export function sleepStopMark(): number {
  return stops;
}

/** Records a skip moving on, so the load it asks for begins from this mark. */
export function noteSkip(): void {
  skip = stops;
}

/** The mark for a load beginning now: the skip's, when one asked for it. */
export function loadSleepMark(): number {
  const pending = skip;
  skip = null;
  return pending ?? stops;
}

/** A mark for playback that has started: any skip still waiting for its load is done. */
export function playingSleepMark(): number {
  skip = null;
  return stops;
}

/** Whether the sleep timer stopped playback after `mark`, so a load must not start itself. */
export function sleepHoldsAutoplay(mark: number): boolean {
  return stops > mark;
}
