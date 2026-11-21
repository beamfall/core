import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("downloads the OPML response as a Blob using same-origin credentials", async () => {
    vi.mocked(fetchSubscriptions).mockResolvedValue([]);
    const blob = new Blob(['<opml version="2.0" />'], { type: "application/xml" });
    const fetchExport = vi.fn().mockResolvedValue({ ok: true, blob: async () => blob });
    vi.stubGlobal("fetch", fetchExport);
    const createObjectURL = vi.fn().mockReturnValue("blob:subscriptions");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", Object.assign(class extends URL {}, { createObjectURL, revokeObjectURL }));
    const downloads: { href: string; filename: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      downloads.push({ href: this.href, filename: this.download });
    });
    const user = userEvent.setup();
    render(
      <I18nProvider initialLocale="fr">
        <SubscriptionsPage />
      </I18nProvider>,
    );
    await user.click(
      screen.getByRole("button", { name: "Exporter les abonnements au format OPML" }),
    );
    expect(fetchExport).toHaveBeenCalledWith("/api/subscriptions/export.opml", {
      credentials: "same-origin",
    });
    expect(createObjectURL).toHaveBeenCalledWith(blob);
    expect(downloads).toEqual([{ href: "blob:subscriptions", filename: "subscriptions.opml" }]);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:subscriptions");
  });

  it.each([
    [401, "Votre session a expiré. Reconnectez-vous pour exporter les abonnements."],
    [500, "Impossible d’exporter les abonnements. Veuillez réessayer."],
    [undefined, "Impossible d’exporter les abonnements. Veuillez réessayer."],
  ])("shows a localized download error for status %s", async (status, message) => {
    vi.mocked(fetchSubscriptions).mockResolvedValue([]);
    const readBody = vi.fn();
    const fetchExport = vi.fn();
    if (status === undefined) fetchExport.mockRejectedValue(new Error("network failure"));
    else fetchExport.mockResolvedValue({ ok: false, status, blob: readBody });
    vi.stubGlobal("fetch", fetchExport);
    const user = userEvent.setup();
    render(
      <I18nProvider initialLocale="fr">
        <SubscriptionsPage />
      </I18nProvider>,
    );
    const button = screen.getByRole("button", { name: "Exporter les abonnements au format OPML" });
    await user.click(button);
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(button).toBeEnabled();
    expect(readBody).not.toHaveBeenCalled();
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
