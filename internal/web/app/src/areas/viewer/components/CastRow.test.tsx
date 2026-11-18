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

function renderRow(credits: MediaCredit[], props: Partial<Parameters<typeof CastRow>[0]> = {}) {
  return render(
    <I18nProvider initialLocale="en">
      <MemoryRouter>
        <CastRow credits={credits} {...props} />
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

  it("names the department even when every credit shares it", () => {
    renderRow([
      credit("p1", "Lead", { attributes: { department: "Acting", character: "Hero" } }),
      credit("p2", "Second", { attributes: { department: "Acting", character: "Friend" } }),
    ]);
    expect(screen.getByRole("heading", { level: 3, name: "Acting" })).toBeInTheDocument();
    expect(within(screen.getByRole("list", { name: "Acting" })).getAllByRole("link")).toHaveLength(
      2,
    );
  });

  it("adds no department heading for credits without one", () => {
    renderRow([credit("p1", "Lead")]);
    expect(screen.queryByRole("heading", { level: 3 })).toBeNull();
  });

  it("previews the first group and links to the full list when people are left out", () => {
    renderRow(
      [
        credit("p1", "Lead", { attributes: { character: "Hero" } }),
        credit("p2", "Second", { attributes: { character: "Friend" } }),
        credit("p3", "Boss", { attributes: { department: "Directing", job: "Director" } }),
      ],
      { preview: { max: 1, to: "/credits/m1" } },
    );
    const list = screen.getByRole("list", { name: "People" });
    expect(
      within(list)
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["LLeadHero"]);
    expect(screen.queryByRole("list", { name: "Directing" })).toBeNull();
    expect(screen.getByRole("link", { name: "See all" })).toHaveAttribute("href", "/credits/m1");
  });

  it("offers no See all when the preview shows everyone", () => {
    renderRow([credit("p1", "Lead")], { preview: { max: 20, to: "/credits/m1" } });
    expect(screen.queryByRole("link", { name: "See all" })).toBeNull();
  });

  it("names the cast group when crew departments follow", () => {
    renderRow(
      [
        credit("p1", "Lead", { attributes: { character: "Hero" } }),
        credit("p2", "Boss", { attributes: { department: "Directing", job: "Director" } }),
      ],
      { castHeading: "Cast" },
    );
    expect(screen.getByRole("heading", { level: 3, name: "Cast" })).toBeInTheDocument();
    expect(within(screen.getByRole("list", { name: "Cast" })).getByRole("link")).toHaveTextContent(
      "LeadHero",
    );
  });

  it("renders nothing without person credits", () => {
    const { container } = renderRow([credit("box", "A Box", { archetype: "container" })]);
    expect(container).toBeEmptyDOMElement();
  });
});
