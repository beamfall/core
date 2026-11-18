import { describe, expect, it } from "vitest";
import type { ScheduleTriggerInfo } from "@/lib/graphql/types";
import { DEFAULT_TRIGGER_HOUR, triggerFieldsFrom, triggerInput } from "./schedule";

function trigger(partial: Partial<ScheduleTriggerInfo>): ScheduleTriggerInfo {
  return { kind: "every", n: 0, unit: "", weekday: 0, hour: 0, minute: 0, ...partial };
}

describe("triggerFieldsFrom (CORE-2445)", () => {
  it("starts the time at 03:00 for an interval trigger", () => {
    const fields = triggerFieldsFrom(trigger({ kind: "every", n: 6, unit: "hours" }));
    expect(fields).toMatchObject({ n: 6, unit: "hours", hour: DEFAULT_TRIGGER_HOUR, minute: 0 });
    expect(triggerInput("daily", fields)).toEqual({ kind: "daily", hour: 3, minute: 0 });
  });

  it("starts the time at 03:00 when there is no trigger", () => {
    expect(triggerFieldsFrom(null)).toMatchObject({ n: 1, hour: 3, minute: 0 });
  });

  it("keeps a saved daily or weekly time, midnight included", () => {
    expect(triggerFieldsFrom(trigger({ kind: "daily", hour: 0, minute: 0 }))).toMatchObject({
      hour: 0,
      minute: 0,
    });
    expect(
      triggerFieldsFrom(trigger({ kind: "weekly", weekday: 2, hour: 22, minute: 15 })),
    ).toMatchObject({ weekday: 2, hour: 22, minute: 15 });
  });
});
