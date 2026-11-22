import "@testing-library/jest-dom/vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchMedia } from "@/areas/viewer/lib/mediaDetail";
import { setIdentityEpochSignal } from "@/lib/api";
import {
  fixMatch,
  fixMatchCandidates,
  fixMatchCurrent,
  fixMatchLockedFields,
  fixMatchPreview,
  fixMatchSources,
} from "@/lib/graphql";
import { fixMatch as transportFixMatch } from "@/lib/graphql/manage";
import { I18nProvider } from "@/lib/i18n";
import { SessionProvider, useSession } from "@/lib/session";
import { viewerCacheSize, writeViewerCache } from "@/lib/viewerCache";
import { AssignDialog } from "./AssignDialog";
import { AssignInterruptionNotice } from "./AssignInterruptionNotice";
import { AssignPanel } from "./AssignPanel";

vi.mock("@/areas/viewer/lib/mediaDetail", () => ({ fetchMedia: vi.fn() }));
vi.mock("@/lib/graphql", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/graphql")>()),
  fixMatch: vi.fn(),
  fixMatchCandidates: vi.fn(),
  fixMatchCurrent: vi.fn(),
  fixMatchLockedFields: vi.fn(),
  fixMatchPreview: vi.fn(),
  fixMatchSources: vi.fn(),
  identifyLookup: vi.fn(),
}));

const movie = { mediaID: "m1", title: "The Thing", year: 1982 };
const catalog = {
  pluginID: "filmtv",
  scraperID: "catalog",
  label: "Catalog",
  acceptsPastedID: true,
  supportsChildren: false,
  isDefault: true,
};
const alternate = {
  ...catalog,
  scraperID: "alternative",
  label: "Alternative Catalog",
  acceptsPastedID: false,
  isDefault: false,
};
const selection = [{ mediaID: "m1" }, { mediaID: "m2" }, { mediaID: "m3" }];
const ids = { pluginID: "filmtv", scraperID: "catalog", candidateID: "thing-1982" };

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(fixMatchSources).mockResolvedValue([alternate, catalog]);
  vi.mocked(fixMatchCurrent).mockResolvedValue({
    pluginID: "filmtv",
    scraperID: "alternative",
    candidateID: "thing-2011",
    title: "The Thing",
    year: 2011,
  });
  vi.mocked(fixMatchCandidates).mockResolvedValue([
    {
      ...ids,
      id: "thing-2011",
      title: "The Thing",
      year: 2011,
      confidence: 0.8,
      scored: true,
      threshold: 0.8,
    },
    {
      ...ids,
      id: ids.candidateID,
      title: "The Thing",
      year: 1982,
      confidence: 0.97,
      scored: true,
      threshold: 0.8,
    },
  ]);
  vi.mocked(fixMatchPreview).mockResolvedValue([
    {
      field: "summary",
      current: "An old overview.",
      proposed: "A corrected overview.",
      changed: true,
    },
  ]);
  vi.mocked(fixMatchLockedFields).mockResolvedValue([{ field: "title", value: "My title" }]);
  vi.mocked(fixMatch).mockImplementation(async ({ mediaID }) => ({
    id: mediaID,
    title: movie.title,
    year: movie.year,
    favorited: false,
  }));
  vi.mocked(fetchMedia).mockImplementation(async (id) => ({
    id,
    title: `Item ${id}`,
    year: movie.year,
    favorited: false,
  }));
});

afterEach(() => {
  vi.unstubAllGlobals();
  setIdentityEpochSignal();
});

async function open(
  selection: React.ComponentProps<typeof AssignDialog>["selection"] = [movie],
  Wrapper?: React.ComponentType<React.PropsWithChildren>,
) {
  const user = userEvent.setup();
  const dialogContent = <AssignDialog selection={selection} />;
  render(
    <I18nProvider>{Wrapper ? <Wrapper>{dialogContent}</Wrapper> : dialogContent}</I18nProvider>,
  );
  await user.click(await screen.findByRole("button", { name: "Assign…" }));
  const dialog = within(await screen.findByRole("dialog", { name: "Assign metadata" }));
  await dialog.findByLabelText("Search title");
  return { user, dialog };
}

async function preview({ user, dialog }: Awaited<ReturnType<typeof open>>) {
  const search = dialog.getByLabelText("Search title");
  search.focus();
  await user.keyboard("{Enter}");
  const candidate = await dialog.findByRole("button", { name: /The Thing \(1982\).*97%/ });
  candidate.focus();
  await user.keyboard("{Enter}");
  expect(dialog.getByRole("button", { name: "Apply" })).toBeDisabled();
  await user.click(dialog.getByRole("button", { name: "Preview changes" }));
}

describe("Assign metadata", () => {
  // feature:assign-media
  it.each([false, true])(
    "re-matches a movie with an explicit preview and clear title lock: %s",
    async (clear) => {
      const view = await open();
      const { user, dialog } = view;
      expect(dialog.getByText("Matched to The Thing (2011) via Alternative Catalog")).toBeVisible();
      expect(dialog.getByLabelText("Metadata source")).toHaveDisplayValue("Catalog");
      expect(dialog.getByLabelText("Search title")).toHaveValue("The Thing");
      expect(dialog.getByLabelText("Year")).toHaveValue("1982");

      const source = dialog.getByLabelText("Metadata source");
      await user.selectOptions(
        source,
        within(source).getByRole("option", { name: "Alternative Catalog" }),
      );
      expect(dialog.queryByText("Know the catalog ID?")).not.toBeInTheDocument();
      await user.selectOptions(source, within(source).getByRole("option", { name: "Catalog" }));
      await preview(view);
      expect(fixMatchCandidates).toHaveBeenCalledWith({
        mediaID: "m1",
        pluginID: "filmtv",
        scraperID: "catalog",
        query: "The Thing",
        year: 1982,
      });
      expect(await dialog.findByText("A corrected overview.")).toBeVisible();
      expect(dialog.getByText("An old overview.")).toBeVisible();
      expect(fixMatch).not.toHaveBeenCalled();
      const checkbox = within(dialog.getByRole("group", { name: "Locked fields" })).getByRole(
        "checkbox",
        { name: /Replace Title with new value/ },
      );
      expect(checkbox).not.toBeChecked();
      expect(dialog.getByText("My title")).toBeVisible();
      if (clear) await user.click(checkbox);
      await user.click(dialog.getByRole("button", { name: "Apply" }));
      expect(await dialog.findByText("Applied")).toBeVisible();
      expect(fixMatch).toHaveBeenCalledWith(
        { mediaID: "m1", ...ids, ...(clear ? { clearLocks: ["title"] } : {}) },
        undefined,
      );
    },
  );

  it("sends a pasted provider ID straight to preview and invalidates the preview if it changes", async () => {
    vi.mocked(fixMatchSources).mockResolvedValue([catalog]);
    vi.mocked(fixMatchCurrent).mockResolvedValue(null);
    const { user, dialog } = await open();
    expect(dialog.getByText("Not matched")).toBeVisible();
    expect(dialog.queryByLabelText("Metadata source")).not.toBeInTheDocument();
    await user.click(dialog.getByText("Know the catalog ID?"));
    const input = dialog.getByLabelText("Paste a provider ID");
    await user.type(input, "tt0084787");
    await user.click(dialog.getByRole("button", { name: "Preview by ID" }));
    expect(await dialog.findByText("A corrected overview.")).toBeVisible();
    expect(fixMatchPreview).toHaveBeenCalledWith({
      mediaID: "m1",
      ...ids,
      candidateID: "tt0084787",
    });
    expect(fixMatchCandidates).not.toHaveBeenCalled();

    await user.type(input, "9");
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    await user.click(dialog.getByRole("button", { name: "Preview by ID" }));
    await dialog.findByText("A corrected overview.");
    await user.click(apply);
    expect(fixMatch).toHaveBeenCalledWith(
      { mediaID: "m1", ...ids, candidateID: "tt00847879" },
      undefined,
    );
  });

  it("confirms a shared candidate, applies in order, and continues after a failed item", async () => {
    vi.mocked(fixMatch).mockImplementation(async ({ mediaID }) => {
      if (mediaID === "m2") throw new Error("Catalog rejected this item");
      return { id: mediaID, title: movie.title, year: movie.year, favorited: false };
    });
    const view = await open(selection);
    await preview(view);
    const { user, dialog } = view;
    const table = within(await dialog.findByRole("table"));
    expect(table.getAllByRole("row")).toHaveLength(4);
    expect(table.getAllByText("The Thing (1982)")).toHaveLength(3);
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    await user.click(dialog.getByRole("checkbox", { name: /I confirm/ }));
    await user.click(apply);
    expect(await table.findByText("Failed: Catalog rejected this item")).toBeVisible();
    expect(within(table.getByRole("row", { name: /Item m1/ })).getByText("Applied")).toBeVisible();
    expect(
      await within(table.getByRole("row", { name: /Item m3/ })).findByText("Applied"),
    ).toBeVisible();
    expect(vi.mocked(fixMatch).mock.calls.map(([vars]) => vars)).toEqual(
      selection.map((item) => ({ ...item, ...ids })),
    );
    expect(fixMatchPreview).toHaveBeenCalledTimes(3);
    expect(fixMatchLockedFields).toHaveBeenCalledTimes(3);
  });

  it.each([
    ["SwitchProfile", 0],
    ["Relock", 0],
    ["Relock", 1],
  ] as const)("stops on %s after %d confirmed changes", async (transition, applied) => {
    const session = { deviceRole: "manager", profileID: "first", profile: "adult", revealed: true };
    const sent: string[] = [];
    let pendingSignal: AbortSignal | null | undefined;
    vi.mocked(fixMatch).mockImplementation(transportFixMatch);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        if (path === "/api/compat/handshake") {
          return Response.json({ decision: "compatible", messageClass: "compat-compatible" });
        }
        if (path === "/api/manifest") return Response.json({});
        const { query, variables } = JSON.parse(String(init?.body));
        if (query.startsWith("query CurrentSession")) {
          return Response.json({ data: { currentSession: session } });
        }
        if (query.startsWith("query Profiles")) return Response.json({ data: { profiles: [] } });
        if (query.startsWith("mutation SwitchProfile")) {
          session.profileID = variables.profileID;
          session.revealed = false;
          return Response.json({ data: { switchProfile: session } });
        }
        if (query.startsWith("mutation Relock")) {
          session.revealed = false;
          return Response.json({ data: { relock: session } });
        }
        if (query.startsWith("mutation FixMatch")) {
          sent.push(variables.mediaID);
          if (sent.length === applied + 1) {
            pendingSignal = init?.signal;
            return new Promise<Response>((_resolve, reject) => {
              pendingSignal?.addEventListener("abort", () => reject(pendingSignal?.reason), {
                once: true,
              });
            });
          }
          return Response.json({ data: { fixMatch: { id: variables.mediaID } } });
        }
        throw new Error(`Unexpected request: ${path} ${query}`);
      }),
    );

    let depart!: () => Promise<void>;
    function Workspace({ children }: React.PropsWithChildren) {
      const { session, switchProfile, relock } = useSession();
      depart = transition === "SwitchProfile" ? () => switchProfile("second") : relock;
      return session ? children : null;
    }
    function Wrapper({ children }: React.PropsWithChildren) {
      return (
        <SessionProvider>
          <AssignInterruptionNotice>
            <Workspace>{children}</Workspace>
          </AssignInterruptionNotice>
        </SessionProvider>
      );
    }

    const batch = selection.slice(0, applied + 2);
    const view = await open(batch, Wrapper);
    const { user, dialog } = view;
    await preview(view);
    await user.click(await dialog.findByRole("checkbox", { name: /I confirm/ }));
    await user.click(dialog.getByRole("button", { name: "Apply" }));
    expect(pendingSignal?.aborted).toBe(false);
    expect(sent).toEqual(batch.slice(0, applied + 1).map(({ mediaID }) => mediaID));

    let departure!: Promise<void>;
    act(() => {
      departure = depart();
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await act(async () => departure);
    expect(pendingSignal?.aborted).toBe(true);
    expect(session.revealed).toBe(false);
    expect(await screen.findByRole("status")).toHaveTextContent(
      `Assignment interrupted. Confirmed changes: ${applied} of ${batch.length}.`,
    );
    expect(sent).toEqual(batch.slice(0, applied + 1).map(({ mediaID }) => mediaID));
    expect(fixMatch).toHaveBeenCalledTimes(applied + 1);
    expect(screen.queryByText(/Failed:|AbortError/)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    await user.click(await screen.findByRole("button", { name: "Assign…" }));
    const reopened = within(await screen.findByRole("dialog", { name: "Assign metadata" }));
    await reopened.findByLabelText("Search title");
    expect(reopened.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(reopened.queryByRole("table")).not.toBeInTheDocument();
    await preview({ user, dialog: reopened });
    expect(fixMatchPreview).toHaveBeenCalledTimes(2 * batch.length);
    expect(await reopened.findByRole("checkbox", { name: /I confirm/ })).not.toBeChecked();
  });

  it("refuses to apply a preview from a departed identity", async () => {
    const epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    const view = await open(selection.slice(0, 2));
    const { user, dialog } = view;
    await preview(view);
    await user.click(await dialog.findByRole("checkbox", { name: /I confirm/ }));
    epoch.abort();
    setIdentityEpochSignal(new AbortController().signal);

    await user.click(dialog.getByRole("button", { name: "Apply" }));
    expect(fixMatch).not.toHaveBeenCalled();
    expect(await dialog.findByRole("status")).toHaveTextContent(
      "Assignment interrupted. Confirmed changes: 0 of 2.",
    );
  });

  it("clears the viewer cache when a write confirms after the panel unmounts", async () => {
    const epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    let confirm: () => void = () => {};
    vi.mocked(fixMatch).mockImplementationOnce(
      ({ mediaID }) =>
        new Promise((resolve) => {
          confirm = () =>
            resolve({ id: mediaID, title: movie.title, year: movie.year, favorited: false });
        }),
    );
    const onFinished = vi.fn();
    const user = userEvent.setup();
    const view = render(
      <I18nProvider>
        <AssignPanel items={[movie]} sources={[catalog]} onFinished={onFinished} />
      </I18nProvider>,
    );
    await preview({
      user,
      dialog: within(screen.getByRole("region", { name: "Assign metadata" })),
    });
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(fixMatch).toHaveBeenCalledOnce();
    writeViewerCache("viewer", "/media/m1", { title: "Old title" });
    view.unmount();
    await act(async () => confirm());

    expect(viewerCacheSize()).toBe(0);
    expect(onFinished).not.toHaveBeenCalled();
  });

  it("omits the interrupted item from the finished results", async () => {
    const epoch = new AbortController();
    setIdentityEpochSignal(epoch.signal);
    vi.mocked(fixMatch).mockImplementationOnce(
      (_, signal) =>
        new Promise((_resolve, reject) => {
          signal?.addEventListener("abort", () => reject(signal.reason), { once: true });
        }),
    );
    const onFinished = vi.fn();
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <AssignPanel
          items={[movie, { mediaID: "m2", title: "Another copy" }]}
          sources={[catalog]}
          onFinished={onFinished}
        />
      </I18nProvider>,
    );
    await preview({
      user,
      dialog: within(screen.getByRole("region", { name: "Assign metadata" })),
    });
    await user.click(await screen.findByRole("checkbox", { name: /I confirm/ }));
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith({ mediaID: "m1", ...ids }, epoch.signal);
    await act(async () => epoch.abort());

    expect(onFinished).toHaveBeenCalledExactlyOnceWith([], 0);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Assignment interrupted. Confirmed changes: 0 of 2.",
    );
  });

  it("blocks a shared candidate for rows with the same structural ordinal", async () => {
    vi.mocked(fetchMedia).mockImplementation(async (id) => ({
      id,
      title: `Item ${id}`,
      year: movie.year,
      index: 0,
      favorited: false,
    }));
    const view = await open(selection);
    await preview(view);
    expect(await view.dialog.findByRole("alert")).toHaveTextContent(
      "Numbered items require their own catalog pairing",
    );
    expect(view.dialog.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(fixMatch).not.toHaveBeenCalled();
  });
});
