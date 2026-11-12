import type { TestInfo } from "@playwright/test";
import { UI_STATE_ANNOTATION, UI_STATES } from "./ui-states.mjs";

const rowIds = new Set<string>(UI_STATES.map((row: { id: string }) => row.id));

/**
 * Declare the ui-states.mjs rows this test covers. Call it after the assertions that prove the
 * state: the coverage reporter counts it only when the test passes. An id the inventory does not
 * have throws here, so a typo fails where it was written.
 */
export function coverUiStates(testInfo: TestInfo, ...ids: string[]): void {
  for (const id of ids) {
    if (!rowIds.has(id)) throw new Error(`coverUiStates: "${id}" is not a row in ui-states.mjs`);
    testInfo.annotations.push({ type: UI_STATE_ANNOTATION, description: id });
  }
}
