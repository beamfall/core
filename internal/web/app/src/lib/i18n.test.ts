import { describe, expect, it } from "vitest";
import { displayString } from "@/lib/display";
import { resolveLocalePreference } from "@/lib/i18n";
import { STRINGS, type StringKey } from "@/strings";

describe("resolveLocalePreference", () => {
  it("keeps the original BCP-47 tag for display strings while choosing a catalog locale", () => {
    const preference = resolveLocalePreference(["fr-CA", "en-US"]);

    expect(preference.locale).toBe("fr");
    expect(preference.displayLocale).toBe("fr-CA");
    expect(displayString({ en: "Libraries", fr: "Bibliothèques" }, preference.displayLocale)).toBe(
      "Bibliothèques",
    );
    expect(
      displayString(
        { en: "Poster", "fr-CA": "Affiche canadienne", fr: "Affiche" },
        preference.displayLocale,
      ),
    ).toBe("Affiche canadienne");
  });

  it("falls back to English catalog strings for unsupported browser locales", () => {
    const preference = resolveLocalePreference(["ja-JP"]);

    expect(preference.locale).toBe("en");
    expect(preference.displayLocale).toBe("ja-JP");
    expect(displayString({ en: "Libraries", fr: "Bibliothèques" }, preference.displayLocale)).toBe(
      "Libraries",
    );
  });

  it("uses English when the browser exposes no locale", () => {
    expect(resolveLocalePreference([])).toEqual({
      locale: "en",
      displayLocale: "en",
    });
  });
});

describe("French Canadian catalog coverage", () => {
  it("translates every plugin lifecycle and uninstall string without English fallback", () => {
    const keys = (Object.keys(STRINGS.en) as StringKey[]).filter(
      (key) => key.startsWith("pluginLifecycle") || key.startsWith("pluginUninstall"),
    );

    expect(keys).toHaveLength(9);
    for (const key of keys) expect(STRINGS.fr[key], key).not.toBe(STRINGS.en[key]);
  });
});
