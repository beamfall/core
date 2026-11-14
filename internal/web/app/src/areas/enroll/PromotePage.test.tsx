import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { I18nProvider } from "@/lib/i18n";
import { PromotePage } from "./PromotePage";

const promote = vi.fn();
vi.mock("@/lib/session", () => ({ useSession: () => ({ promote }) }));

afterEach(() => {
  cleanup();
  promote.mockReset();
});

async function submit(code: string) {
  render(
    <MemoryRouter>
      <I18nProvider>
        <PromotePage />
      </I18nProvider>
    </MemoryRouter>,
  );
  if (code) await userEvent.type(screen.getByLabelText("Pairing code"), code);
  await userEvent.click(screen.getByRole("button", { name: "Promote" }));
}

describe("PromotePage", () => {
  it("says a viewer code can't promote, rather than calling it invalid", async () => {
    promote.mockRejectedValue(new ApiError("POST /api/web/promote failed", 403));

    await submit("abcd 1234");

    expect(promote).toHaveBeenCalledWith("ABCD1234");
    expect(screen.getByRole("alert")).toHaveTextContent("That's a viewer code");
  });

  it("asks for a code instead of submitting an empty one", async () => {
    await submit("");

    expect(promote).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("Enter the pairing code to continue.");
  });
});
