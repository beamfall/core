import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { addSubscription, fetchSubscriptions } from "@/lib/graphql";
import type { SubscriptionInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { SubscriptionsPage } from "./SubscriptionsPage";

vi.mock("@/lib/graphql", () => ({
  addSubscription: vi.fn(),
  fetchSubscriptions: vi.fn(),
  removeSubscription: vi.fn(),
  setSubscriptionRetention: vi.fn(),
}));

vi.mock("@/lib/session", () => ({
  useSession: () => ({
    manifest: {
      libraries: [
        { id: "library-a", title: "Library A" },
        { id: "library-b", title: "Library B" },
      ],
    },
  }),
}));

describe("SubscriptionsPage", () => {
  beforeEach(() => vi.clearAllMocks());

  it("does not let an older library response replace the current library", async () => {
    let resolveA: ((value: SubscriptionInfo[]) => void) | undefined;
    vi.mocked(fetchSubscriptions).mockImplementation((libraryID) => {
      if (libraryID === "library-a") {
        return new Promise((resolve) => {
          resolveA = resolve;
        }) as ReturnType<typeof fetchSubscriptions>;
      }
      return Promise.resolve([
        {
          id: "sub-b",
          title: "Beta feed",
          feedURL: "https://b.test/feed",
          root: "",
          retentionKind: "keep-all",
          retentionN: null,
        },
      ]) as ReturnType<typeof fetchSubscriptions>;
    });
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <SubscriptionsPage />
      </I18nProvider>,
    );
    await waitFor(() => expect(fetchSubscriptions).toHaveBeenCalledWith("library-a"));

    await user.click(screen.getByRole("combobox", { name: "Library" }));
    await user.click(await screen.findByRole("option", { name: "Library B" }));
    expect(await screen.findByText("Beta feed")).toBeInTheDocument();

    resolveA?.([
      {
        id: "sub-a",
        title: "Alpha feed",
        feedURL: "https://a.test/feed",
        root: "",
        retentionKind: "keep-all",
        retentionN: null,
      },
    ]);
    await waitFor(() => expect(screen.queryByText("Alpha feed")).not.toBeInTheDocument());
    expect(screen.getByText("Beta feed")).toBeInTheDocument();
  });

  it("does not refresh or commit an add result after switching libraries", async () => {
    vi.mocked(fetchSubscriptions).mockImplementation((libraryID) =>
      Promise.resolve([
        {
          id: `sub-${libraryID}`,
          title: libraryID === "library-a" ? "Alpha feed" : "Beta feed",
          feedURL: `https://${libraryID}.test/feed`,
          root: "",
          retentionKind: "keep-all",
          retentionN: null,
        },
      ]),
    );
    let resolveAdd: (() => void) | undefined;
    vi.mocked(addSubscription).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveAdd = () => resolve({ id: "new-subscription" });
        }),
    );
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <SubscriptionsPage />
      </I18nProvider>,
    );
    expect(await screen.findByText("Alpha feed")).toBeInTheDocument();
    const feedURL = screen.getByLabelText("Feed URL");
    await user.type(feedURL, "https://new.test/feed");
    await user.click(screen.getByRole("button", { name: "Add Subscription" }));
    await waitFor(() => expect(addSubscription).toHaveBeenCalledOnce());

    await user.click(screen.getByRole("combobox", { name: "Library" }));
    await user.click(await screen.findByRole("option", { name: "Library B" }));
    expect(await screen.findByText("Beta feed")).toBeInTheDocument();
    resolveAdd?.();

    await waitFor(() =>
      expect(vi.mocked(fetchSubscriptions).mock.calls.map(([id]) => id)).toEqual([
        "library-a",
        "library-b",
      ]),
    );
    expect(feedURL).toHaveValue("https://new.test/feed");
    expect(screen.getByText("Beta feed")).toBeInTheDocument();
  });
});
