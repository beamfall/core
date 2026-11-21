import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { formatMessage, type useT } from "@/lib/i18n";
import { STRINGS } from "@/strings";
import { formatServerBuild, useServerBuild } from "./serverBuild";

const t: ReturnType<typeof useT> = (key, vars) => formatMessage(STRINGS.en[key], vars, "en-GB");
const buildDate = "2026-11-21T00:15:00Z";
const commit = "0123456789abcdef0123456789abcdef01234567";

describe("formatServerBuild", () => {
  it("formats a release date in the display locale and keeps full identity in the details", () => {
    const result = formatServerBuild({ version: "v1.2.3", commit, buildDate }, "en-GB", t);
    expect(result.label).toBe("v1.2.3 · 21 Nov 2026");
    expect(result.details).toContain("Version v1.2.3");
    expect(result.details).toContain(`Build date ${buildDate}`);
    expect(result.details).toContain(`Commit ${commit}`);
  });

  it("shortens a development build and commit", () => {
    const result = formatServerBuild({ version: "development", commit, buildDate }, "en-GB", t);
    expect(result.label).toBe("dev 0123456 · 21 Nov 2026");
    expect(result.details).toContain("Version development");
    expect(result.details).toContain(commit);
  });

  it("shows only the version when the date is unknown", () => {
    const result = formatServerBuild(
      { version: "v1.2.3", commit: null, buildDate: null },
      "en-GB",
      t,
    );
    expect(result).toEqual({ label: "v1.2.3", details: "Version v1.2.3" });
  });

  it("shortens a Docker development version without a commit", () => {
    const version = "development+src.0123456789ab";
    const result = formatServerBuild({ version, commit: null, buildDate: null }, "en-GB", t);
    expect(result).toEqual({ label: "dev", details: `Version ${version}` });
  });
});

afterEach(() => vi.unstubAllGlobals());

it("shares a not-ready health response across shells and remounts", async () => {
  const build = { version: "v1.2.3", commit, buildDate };
  const fetchHealth = vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify({ capabilities: build }), { status: 503 }));
  vi.stubGlobal("fetch", fetchHealth);

  const first = renderHook(useServerBuild);
  const second = renderHook(useServerBuild);
  await waitFor(() => {
    expect(first.result.current).toEqual(build);
    expect(second.result.current).toEqual(build);
  });
  first.unmount();
  second.unmount();
  const remount = renderHook(useServerBuild);
  expect(remount.result.current).toEqual(build);
  expect(fetchHealth).toHaveBeenCalledExactlyOnceWith("/health", {
    credentials: "same-origin",
    cache: "no-store",
  });
  remount.unmount();
});
