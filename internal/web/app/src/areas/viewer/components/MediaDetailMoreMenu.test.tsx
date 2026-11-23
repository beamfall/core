import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { MediaDetailMoreMenu } from "./MediaDetailMoreMenu";

const session = vi.hoisted(() => ({ value: { isManager: true, session: { profile: "adult" } } }));

vi.mock("@/lib/session", () => ({ useSession: () => session.value }));
vi.mock("../actions", () => ({
  useItemHide: () => ({ hidden: false, pending: false, failed: false, toggle: vi.fn() }),
}));
vi.mock("./EditMediaDetailsDialog", () => ({
  EditMediaDetailsDialog: ({ title }: { title: string }) => (
    <div role="dialog">editing {title}</div>
  ),
}));

function renderMenu() {
  render(
    <I18nProvider>
      <MediaDetailMoreMenu mediaID="media-1" title="Sample Film" />
    </I18nProvider>,
  );
}

beforeEach(() => {
  session.value = { isManager: true, session: { profile: "adult" } };
});

describe("MediaDetailMoreMenu Edit details", () => {
  it("offers Edit details to the admin and opens the editor", async () => {
    renderMenu();
    await userEvent.click(screen.getByRole("button", { name: /Sample Film/ }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Edit details" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("editing Sample Film");
  });

  it.each([
    ["a non-manager device", { isManager: false, session: { profile: "adult" } }],
    ["a child Profile", { isManager: true, session: { profile: "child" } }],
  ])("hides Edit details on %s", async (_name, value) => {
    session.value = value;
    renderMenu();
    await userEvent.click(screen.getByRole("button", { name: /Sample Film/ }));
    await screen.findByRole("menuitem", { name: /Copy link/i });
    expect(screen.queryByRole("menuitem", { name: "Edit details" })).toBeNull();
  });
});
