import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { enroll } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { EnrollPage } from "./EnrollPage";

vi.mock("@/lib/graphql", () => ({
  enroll: vi.fn(),
}));

const assign = vi.fn();

beforeEach(() => {
  vi.mocked(enroll).mockReset();
  assign.mockReset();
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { ...window.location, assign },
  });
});

afterEach(() => {
  cleanup();
});

async function submit(code: string) {
  render(
    <I18nProvider>
      <EnrollPage />
    </I18nProvider>,
  );
  await userEvent.type(screen.getByLabelText("Pairing code"), code);
  await userEvent.click(screen.getByRole("button", { name: "Connect" }));
}

describe("EnrollPage", () => {
  it("redirects to the app root and reports the connection when the rebind succeeds", async () => {
    vi.mocked(enroll).mockResolvedValue("bound");

    await submit("ABCD-1234");

    expect(enroll).toHaveBeenCalledWith("ABCD-1234", "Web Browser");
    expect(assign).toHaveBeenCalledWith("/");
    expect(screen.getByRole("status")).toHaveTextContent("Connected.");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("still redirects after a 201 whose rebind rejected, with a handshake notice", async () => {
    vi.mocked(enroll).mockResolvedValue("rebind-failed");

    await submit("ABCD-1234");

    expect(assign).toHaveBeenCalledWith("/");
    expect(screen.getByRole("status")).toHaveTextContent("server handshake did not complete");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("reports an invalid code without redirecting", async () => {
    vi.mocked(enroll).mockRejectedValue(new ApiError("POST /api/web/enroll failed", 401));

    await submit("WRONG-CODE");

    expect(assign).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("That pairing code is not valid.");
    expect(screen.getByRole("button", { name: "Connect" })).toBeEnabled();
  });
});
