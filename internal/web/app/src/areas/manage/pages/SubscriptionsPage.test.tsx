import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { addSubscription, fetchSubscriptions, setSubscriptionRetention } from "@/lib/graphql";
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

  it("links to the default OPML download with a localized label", async () => {
    vi.mocked(fetchSubscriptions).mockResolvedValue([]);
    render(
      <I18nProvider initialLocale="fr">
        <SubscriptionsPage />
      </I18nProvider>,
    );
    const link = screen.getByRole("link", { name: "Exporter les abonnements au format OPML" });
    expect(link).toHaveAttribute("href", "/api/subscriptions/export.opml");
    expect(link).toHaveAttribute("download", "subscriptions.opml");
    await waitFor(() => expect(fetchSubscriptions).toHaveBeenCalledWith("library-a"));
  });

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
  it("offers download counts for Keep only the newest, defaulting to 10", async () => {
    vi.mocked(fetchSubscriptions).mockResolvedValue([
      {
        id: "sub-a",
        title: "Alpha feed",
        feedURL: "https://a.test/feed",
        root: "",
        retentionKind: "keep-all",
        retentionN: null,
      },
    ]);
    vi.mocked(setSubscriptionRetention).mockResolvedValue(undefined as never);
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <SubscriptionsPage />
      </I18nProvider>,
    );
    expect(await screen.findByText("Alpha feed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Help: Keep downloads" })).toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Keep downloads" }));
    await user.click(await screen.findByRole("option", { name: "Keep only the newest" }));
    const count = screen.getByRole("combobox", { name: "Downloads to keep" });
    expect(count).toHaveTextContent("10");

    await user.click(count);
    await user.click(await screen.findByRole("option", { name: "25" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(setSubscriptionRetention).toHaveBeenCalledWith("sub-a", "keep-last-n", 25),
    );
  });

  it("keeps a saved download count that is not one of the choices", async () => {
    vi.mocked(fetchSubscriptions).mockResolvedValue([
      {
        id: "sub-a",
        title: "Alpha feed",
        feedURL: "https://a.test/feed",
        root: "",
        retentionKind: "keep-last-n",
        retentionN: 7,
      },
    ]);
    render(
      <I18nProvider>
        <SubscriptionsPage />
      </I18nProvider>,
    );
    expect(await screen.findByRole("combobox", { name: "Downloads to keep" })).toHaveTextContent(
      "7",
    );
  });
});
