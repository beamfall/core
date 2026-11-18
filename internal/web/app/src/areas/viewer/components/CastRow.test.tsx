import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it } from "vitest";
import type { MediaCredit } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { CastRow } from "./CastRow";

afterEach(cleanup);

const credit = (
  entityID: string,
  title: string,
  extra: Partial<MediaCredit> = {},
): MediaCredit => ({
  entityID,
  title,
  kind: "person",
  archetype: "person",
  relation: "credited-in",
  ...extra,
});

function renderRow(credits: MediaCredit[]) {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <CastRow credits={credits} />
      </MemoryRouter>
    </I18nProvider>,
  );
}

describe("CastRow", () => {
  it("lists people in credit order with headshot, name and part, linking to the person page", () => {
    renderRow([
      credit("p1", "Zed Lead", {
        attributes: { character: "The Hero" },
        artworkSlots: { poster: "/api/entity/artwork/p1/poster?v=1" },
      }),
      credit("p2", "Amy Support"),
      credit("box", "A Box", { archetype: "container" }),
    ]);
    expect(screen.getByRole("heading", { name: "People" })).toBeInTheDocument();
    const list = screen.getByRole("list", { name: "People" });
    const links = within(list).getAllByRole("link");
    expect(links.map((link) => link.textContent)).toEqual(["Zed LeadThe Hero", "AAmy Support"]);
    expect(links[0]).toHaveAttribute("href", "/entity/p1");
    const img = links[0].querySelector("img");
    expect(img?.getAttribute("src")).toBe("/api/entity/artwork/p1/poster?v=1&bucket=w320");
    expect(links[1].querySelector("img")).toBeNull();
  });

  it("groups people by department when credits carry several", () => {
    renderRow([
      credit("p1", "Lead", { attributes: { department: "Acting", character: "Hero" } }),
      credit("p2", "Boss", { attributes: { department: "Directing", job: "Director" } }),
    ]);
    expect(screen.getByRole("heading", { name: "Acting" })).toBeInTheDocument();
    const directing = screen.getByRole("list", { name: "Directing" });
    expect(within(directing).getByRole("link")).toHaveTextContent("BossDirector");
  });

  it("renders nothing without person credits", () => {
    const { container } = renderRow([credit("box", "A Box", { archetype: "container" })]);
    expect(container).toBeEmptyDOMElement();
  });
});
