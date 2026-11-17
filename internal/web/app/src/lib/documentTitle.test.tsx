import "@testing-library/jest-dom/vitest";
import { act, cleanup, render } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { pageDocumentTitle, useDocumentTitle, useHeadingDocumentTitle } from "./documentTitle";

afterEach(() => {
  cleanup();
  document.title = "";
});

describe("document title (CORE-2322)", () => {
  it("names the page before the brand, and falls back to the brand", () => {
    expect(pageDocumentTitle("Films", "Beamfall")).toBe("Films · Beamfall");
    expect(pageDocumentTitle("  Blade\n Runner ", "Beamfall")).toBe("Blade Runner · Beamfall");
    expect(pageDocumentTitle("", "Beamfall")).toBe("Beamfall");
    expect(pageDocumentTitle(undefined, "Beamfall")).toBe("Beamfall");
    expect(pageDocumentTitle("Beamfall", "Beamfall")).toBe("Beamfall");
  });

  it("follows the main region's heading as it loads and changes", async () => {
    let setHeading: (value: string) => void = () => {};
    function Shell() {
      const [heading, set] = useState("");
      setHeading = set;
      useHeadingDocumentTitle("main", "Beamfall");
      return <main id="main">{heading ? <h1>{heading}</h1> : <p>Loading</p>}</main>;
    }
    render(<Shell />);
    expect(document.title).toBe("Beamfall");

    await act(async () => setHeading("Arrival"));
    expect(document.title).toBe("Arrival · Beamfall");

    await act(async () => setHeading("Search"));
    expect(document.title).toBe("Search · Beamfall");
  });

  it("shows the brand alone when the page must stay unnamed (a Revealed session)", () => {
    function Shell() {
      useHeadingDocumentTitle("main", "Beamfall", false);
      return (
        <main id="main">
          <h1>Restricted title</h1>
        </main>
      );
    }
    document.title = "Earlier · Beamfall";
    render(<Shell />);
    expect(document.title).toBe("Beamfall");
  });

  it("sets an explicit title and leaves the tab alone for null", () => {
    function Page({ title }: { title: string | null }) {
      useDocumentTitle(title, "Beamfall");
      return null;
    }
    const { rerender } = render(<Page title="Arrival" />);
    expect(document.title).toBe("Arrival · Beamfall");
    document.title = "Elsewhere";
    rerender(<Page title={null} />);
    expect(document.title).toBe("Elsewhere");
  });
});
