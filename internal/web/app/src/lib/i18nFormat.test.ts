import { describe, expect, it } from "vitest";
import { formatMessage } from "@/lib/i18n";

// Locale-aware number/plural formatting behind the text-only `t` API
// (ADR-0034). `formatMessage` is the engine `t` calls with the active display
// locale; these exercise it directly across every supported display locale
// (`fr-CA` is a display locale that maps to the `fr` catalog).
const LOCALES = ["en", "fr-CA", "de", "es", "pt"] as const;

describe("formatMessage — legacy interpolation is unchanged", () => {
  it("replaces bare {name} placeholders exactly as before", () => {
    expect(formatMessage("Hello {name}", { name: "Ada" }, "en")).toBe("Hello Ada");
  });

  it("renders unknown numeric vars with plain String(), no grouping", () => {
    expect(formatMessage("count {count}", { count: 1000 }, "de")).toBe("count 1000");
  });

  it("leaves free-standing apostrophes untouched in legacy strings", () => {
    // No typed argument -> legacy path -> ICU apostrophe quoting must NOT apply.
    expect(formatMessage("don't stop {name}", { name: "now" }, "fr-CA")).toBe("don't stop now");
  });

  it("keeps a missing placeholder literal", () => {
    expect(formatMessage("hi {who}", { name: "x" }, "en")).toBe("hi {who}");
  });

  it("returns the template verbatim when no vars are supplied", () => {
    expect(formatMessage("l'ouverture {n, number}", undefined, "fr-CA")).toBe(
      "l'ouverture {n, number}",
    );
  });
});

describe("formatMessage — number", () => {
  it("formats 1,000+ per locale via Intl.NumberFormat", () => {
    for (const locale of LOCALES) {
      for (const n of [1000, 12345, 1_000_000]) {
        expect(formatMessage("{n, number}", { n }, locale)).toBe(
          new Intl.NumberFormat(locale).format(n),
        );
      }
    }
  });

  it("uses locale-specific grouping (en comma vs de dot)", () => {
    expect(formatMessage("{n, number}", { n: 1000 }, "en")).toBe("1,000");
    expect(formatMessage("{n, number}", { n: 1_000_000 }, "de")).toBe("1.000.000");
  });

  it("falls back to English formatting for a malformed locale", () => {
    expect(formatMessage("{n, number}", { n: 1000 }, "!!not-a-locale!!")).toBe("1,000");
  });
});

describe("formatMessage — plural", () => {
  const msg = "{count, plural, =0 {no items} one {# item} other {# items}}";

  it("selects the 0/1/2 branches for English", () => {
    expect(formatMessage(msg, { count: 0 }, "en")).toBe("no items");
    expect(formatMessage(msg, { count: 1 }, "en")).toBe("1 item");
    expect(formatMessage(msg, { count: 2 }, "en")).toBe("2 items");
  });

  it("covers CLDR categories for 0/1/2 across every locale", () => {
    const branches =
      "{count, plural, =0 {EXACT0} zero {ZERO} one {ONE} two {TWO} few {FEW} many {MANY} other {OTHER}}";
    const byCategory: Record<string, string> = {
      zero: "ZERO",
      one: "ONE",
      two: "TWO",
      few: "FEW",
      many: "MANY",
      other: "OTHER",
    };
    for (const locale of LOCALES) {
      expect(formatMessage(branches, { count: 0 }, locale)).toBe("EXACT0"); // exact wins
      for (const count of [1, 2]) {
        const category = new Intl.PluralRules(locale).select(count);
        expect(formatMessage(branches, { count }, locale)).toBe(byCategory[category]);
      }
    }
  });

  it("formats the # token with locale grouping inside a branch", () => {
    for (const locale of LOCALES) {
      expect(formatMessage("{count, plural, other {# items}}", { count: 1000 }, locale)).toBe(
        `${new Intl.NumberFormat(locale).format(1000)} items`,
      );
    }
  });

  it("falls back to English plural rules for a malformed locale", () => {
    expect(formatMessage(msg, { count: 1 }, "")).toBe("1 item");
    expect(formatMessage(msg, { count: 3 }, "??")).toBe("3 items");
  });
});

describe("formatMessage — escaping", () => {
  it("quotes literal braces with apostrophes on the ICU path", () => {
    expect(formatMessage("'{'literal'}' {n, number}", { n: 5 }, "en")).toBe("{literal} 5");
  });

  it("quotes a literal # inside a plural branch", () => {
    expect(formatMessage("{count, plural, other {'#' = # left}}", { count: 3 }, "en")).toBe(
      "# = 3 left",
    );
  });

  it("renders a doubled apostrophe as a single one when the string opts in", () => {
    expect(formatMessage("it''s {n, number}", { n: 3 }, "en")).toBe("it's 3");
    expect(
      formatMessage(
        "{count, plural, one {It''s # item} other {They''re # items}}",
        { count: 1 },
        "en",
      ),
    ).toBe("It's 1 item");
  });
});
