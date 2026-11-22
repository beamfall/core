import { expect, test } from "./lib/test";

test("opens Assign on a matched item and previews a replacement", async ({ page }) => {
  const source = { pluginID: "fixture-catalog", scraperID: "catalog" };
  // The development seed's Media and manager session are real. Its library has
  // no external catalog, so supply deterministic responses for the Assign reads.
  const responses: Record<string, object> = {
    FixMatchSources: {
      fixMatchSources: [
        {
          ...source,
          label: "Fixture catalog",
          supportsChildren: false,
          acceptsPastedID: true,
          isDefault: true,
        },
      ],
    },
    FixMatchCurrent: {
      fixMatchCurrent: { ...source, candidateID: "old", title: "Sample Media", year: 2024 },
    },
    FixMatchCandidates: {
      fixMatchCandidates: [
        {
          ...source,
          id: "replacement",
          title: "Sample Media Remastered",
          year: 2024,
          confidence: 0.98,
          threshold: 0.8,
          scored: true,
        },
      ],
    },
    FixMatchPreview: {
      fixMatchPreview: [
        {
          field: "title",
          current: "Sample Media",
          proposed: "Sample Media Remastered",
          changed: true,
        },
      ],
    },
    FixMatchLockedFields: { fixMatchLockedFields: [] },
  };
  await page.route("**/api/graphql", async (route) => {
    const { query } = route.request().postDataJSON() as { query: string };
    const operation = /^query (\w+)/.exec(query)?.[1];
    if (operation && responses[operation]) {
      await route.fulfill({ json: { data: responses[operation] } });
    } else {
      await route.fallback();
    }
  });

  await page.goto("/web/media/media-alpha");
  await page.getByRole("button", { name: "Assign…" }).click();
  const dialog = page.getByRole("dialog", { name: "Assign metadata" });
  await expect(
    dialog.getByText("Matched to Sample Media (2024) via Fixture catalog"),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Search", exact: true }).click();
  await dialog.getByRole("button", { name: /Sample Media Remastered.*98%/ }).click();
  await expect(dialog.getByRole("button", { name: "Apply", exact: true })).toBeDisabled();
  await dialog.getByRole("button", { name: "Preview changes" }).click();
  await expect(dialog.getByRole("heading", { name: "Field changes" })).toBeVisible();
  await expect(dialog.getByRole("group", { name: "Locked fields" })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Apply", exact: true })).toBeEnabled();
});
