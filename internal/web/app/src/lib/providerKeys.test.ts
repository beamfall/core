import { describe, expect, it } from "vitest";
import type { PluginScraperInfo } from "@/lib/graphql/types";
import { groupProviderKeys, keyNeeded, providerKeyLabel } from "./providerKeys";

function scraper(over: Partial<PluginScraperInfo>): PluginScraperInfo {
  return {
    pluginID: "meta",
    scraperID: "s",
    displayName: "",
    kind: "metadata",
    endpoint: "https://api.example.org/v3",
    authRef: "example-key",
    authStyle: "query",
    credentialRequired: true,
    credentialConfigured: false,
    credentialStatus: "missing",
    ...over,
  };
}

describe("providerKeyLabel", () => {
  it("collapses qualifiers shared by one provider", () => {
    expect(providerKeyLabel(["Example (Movies)", "Example (TV)"], "ref")).toBe("Example");
  });
  it("strips the qualifier from a single scraper name", () => {
    expect(providerKeyLabel(["OtherDB (Series)"], "ref")).toBe("OtherDB");
  });
  it("uses the common prefix of differing names", () => {
    expect(providerKeyLabel(["Acme Books", "Acme Covers"], "ref")).toBe("Acme");
  });
  it("falls back to the first name, then the AuthRef", () => {
    expect(providerKeyLabel(["Alpha", "Beta"], "ref")).toBe("Alpha");
    expect(providerKeyLabel([""], "ref")).toBe("ref");
  });
});

describe("groupProviderKeys", () => {
  it("groups by AuthRef with hosts and status", () => {
    const keys = groupProviderKeys([
      scraper({ scraperID: "a", displayName: "Example (Movies)" }),
      scraper({ scraperID: "b", displayName: "Example (TV)" }),
      scraper({ scraperID: "c", authRef: "", credentialRequired: false }),
      scraper({
        scraperID: "d",
        displayName: "Other",
        authRef: "other-key",
        endpoint: "https://other.example.net",
        credentialStatus: "configured",
      }),
    ]);
    expect(keys).toHaveLength(2);
    expect(keys[0]).toMatchObject({
      authRef: "example-key",
      label: "Example",
      hosts: ["api.example.org"],
      required: true,
      status: "missing",
    });
    expect(keyNeeded(keys[0])).toBe(true);
    expect(keyNeeded(keys[1])).toBe(false);
  });

  it("does not flag a key the owner disabled as needed", () => {
    // CORE-1540: disabling stops the provider on purpose; it is not a setup gap.
    const [key] = groupProviderKeys([scraper({ credentialStatus: "disabled" })]);
    expect(key.status).toBe("disabled");
    expect(keyNeeded(key)).toBe(false);
  });

  it("carries plugin-declared credential help from any row of the AuthRef", () => {
    // CORE-1127: a plugin names its credential and links where to get one.
    const help = {
      label: { en: "Subscriber PIN" },
      helpURL: "https://subscribe.example.org",
      helpLabel: { en: "Subscribe at example.org" },
    };
    const [key, other] = groupProviderKeys([
      scraper({ scraperID: "a" }),
      scraper({ scraperID: "b", credentialHelp: help }),
      scraper({ scraperID: "c", authRef: "other-key" }),
    ]);
    expect(key.credentialHelp).toEqual(help);
    expect(other.credentialHelp).toBeUndefined();
  });
});
