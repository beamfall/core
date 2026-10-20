import type { Page } from "@playwright/test";

const focusSectionItems = {
  "recently-added": [
    {
      id: "focus-recent",
      kind: "media",
      title: "Sample Media",
      summary: "1 play",
      artwork: { poster: "" },
      artworkSlots: [],
    },
  ],
  "most-played": [
    {
      id: "focus-most-played",
      kind: "media",
      title: "Sample Media",
      summary: "1 play",
      artwork: { poster: "" },
      artworkSlots: [],
    },
  ],
} as const;

export async function seedViewerFocusSections(page: Page): Promise<void> {
  await page.route("**/api/graphql", async (route) => {
    const body = route.request().postDataJSON() as {
      query?: string;
      variables?: { kind?: string };
    };
    const kind = body.variables?.kind as keyof typeof focusSectionItems;
    const items = focusSectionItems[kind];
    if (!body.query?.includes("query SourceItems") || !items) {
      await route.continue();
      return;
    }
    await route.fulfill({ json: { data: { sourceItems: items } } });
  });
}
