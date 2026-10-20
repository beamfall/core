import { describe, expect, it } from "vitest";
import {
  changedRenames,
  DEFAULT_PATTERN,
  previewRenames,
  type RenameSource,
} from "./renamePattern";

const sources: RenameSource[] = [
  { path: "/srv/a/clip one.mp4", name: "clip one.mp4" },
  { path: "/srv/a/clip two.mkv", name: "clip two.mkv" },
];

describe("previewRenames", () => {
  it("is a no-op under the default pattern", () => {
    const out = previewRenames(sources, DEFAULT_PATTERN);
    expect(out.map((p) => p.newName)).toEqual(["clip one.mp4", "clip two.mkv"]);
    expect(out.every((p) => !p.changed)).toBe(true);
  });

  it("applies literal find/replace and preserves the extension", () => {
    const out = previewRenames(sources, {
      ...DEFAULT_PATTERN,
      find: "clip",
      replace: "shot",
    });
    expect(out[0].newName).toBe("shot one.mp4");
    expect(out[1].newName).toBe("shot two.mkv");
    expect(out.every((p) => p.changed)).toBe(true);
  });

  it("numbers with a padded {n} prefix template", () => {
    const out = previewRenames(sources, {
      ...DEFAULT_PATTERN,
      prefix: "Ep{n} - ",
      startAt: 1,
      pad: 2,
    });
    expect(out[0].newName).toBe("Ep01 - clip one.mp4");
    expect(out[1].newName).toBe("Ep02 - clip two.mkv");
  });

  it("appends the number when the prefix has no {n}", () => {
    const out = previewRenames([sources[0]], {
      ...DEFAULT_PATTERN,
      prefix: "Episode ",
      startAt: 3,
      pad: 0,
    });
    expect(out[0].newName).toBe("Episode 3 clip one.mp4");
  });

  it("handles dotfiles and extensionless names without dropping content", () => {
    const out = previewRenames(
      [
        { path: "/srv/.env", name: ".env" },
        { path: "/srv/README", name: "README" },
      ],
      { ...DEFAULT_PATTERN, find: "R", replace: "X" },
    );
    expect(out[0].newName).toBe(".env");
    expect(out[1].newName).toBe("XEADME");
  });
});

describe("changedRenames", () => {
  it("emits only the (path, name) pairs that actually change", () => {
    const previews = previewRenames(sources, {
      ...DEFAULT_PATTERN,
      find: "one",
      replace: "uno",
    });
    expect(changedRenames(previews)).toEqual([
      { path: "/srv/a/clip one.mp4", name: "clip uno.mp4" },
    ]);
  });
});
