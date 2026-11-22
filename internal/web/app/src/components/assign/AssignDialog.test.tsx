import "@testing-library/jest-dom/vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchMedia } from "@/areas/viewer/lib/mediaDetail";
import { setIdentityEpochSignal } from "@/lib/api";
import {
  fixMatch,
  fixMatchCandidates,
  fixMatchChildren,
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
  fixMatchChildren: vi.fn(),
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
  childLevels: [],
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
      expect(fixMatchChildren).not.toHaveBeenCalled();
      expect(dialog.queryByRole("heading", { name: "Season" })).not.toBeInTheDocument();
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
        { mediaID: "m1", ...ids, clearLocks: clear ? ["title", "ordinal"] : ["ordinal"] },
        undefined,
      );
      // Toggling another field's lock never changes whether the ordinal is
      // cleared: preview and Apply both name it explicitly.
      expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith({
        mediaID: "m1",
        ...ids,
        clearLocks: ["ordinal"],
      });
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
      clearLocks: ["ordinal"],
    });
    expect(fixMatchCandidates).not.toHaveBeenCalled();

    await user.type(input, "9");
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    await user.click(dialog.getByRole("button", { name: "Preview by ID" }));
    await dialog.findByText("A corrected overview.");
    await user.click(apply);
    expect(fixMatch).toHaveBeenCalledWith(
      { mediaID: "m1", ...ids, candidateID: "tt00847879", clearLocks: ["ordinal"] },
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
      selection.map((item) => ({ ...item, ...ids, clearLocks: ["ordinal"] })),
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
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(
      { mediaID: "m1", ...ids, clearLocks: ["ordinal"] },
      epoch.signal,
    );
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

describe("Assign catalog children", () => {
  const show = { ...ids, candidateID: "show" };
  const episodes = [
    { key: "e1", label: "Episode One", sequence: "2", index: 1, artworkURL: "/art/e1" },
    { key: "e2", label: "Episode Two", sequence: "2", index: 2 },
    { key: "e3", label: "Episode Three", sequence: "2", index: 3 },
    { key: "e5", label: "Episode Five", sequence: "2", index: 5, year: 2024 },
    { key: "e0", label: "Unnumbered Special", sequence: "2", index: null },
  ];
  const season = { key: "s2", label: "Second season", sequence: "2", year: 2024 };
  const specials = { key: "s0", label: "Specials", sequence: "0" };
  const childSource = { ...catalog, supportsChildren: true, childLevels: ["season", "episode"] };

  beforeEach(() => {
    vi.mocked(fixMatchSources).mockResolvedValue([childSource]);
    vi.mocked(fixMatchCurrent).mockResolvedValue(null);
    vi.mocked(fixMatchCandidates).mockResolvedValue([
      {
        ...ids,
        id: show.candidateID,
        title: "Catalog show",
        year: 2023,
        artwork: { poster: "/art/show" },
        confidence: 0.9,
        scored: true,
        threshold: 0.8,
      },
    ]);
    vi.mocked(fixMatchChildren).mockImplementation(async ({ level, parentKey }) => {
      if (level === 1) return [season, specials];
      if (parentKey === "s0")
        return [{ key: "s0e1", label: "Holiday episode", sequence: "0", index: 1 }];
      return episodes;
    });
  });

  async function pickCandidate({ user, dialog }: Awaited<ReturnType<typeof open>>) {
    await user.click(dialog.getByRole("button", { name: "Search" }));
    await user.click(await dialog.findByRole("button", { name: /Catalog show/ }));
  }

  it.each([
    { parent: /Second season/, key: "s2", child: /Episode Five/, sequence: "2", index: 5 },
    { parent: /Specials/, key: "s0", child: /Holiday episode/, sequence: "0", index: 1 },
  ])("previews and applies a selected child from sequence $sequence", async (row) => {
    const view = await open([{ mediaID: "m1", title: "Catalog show", sequence: 1, index: 3 }]);
    const { user, dialog } = view;
    await pickCandidate(view);
    expect(fixMatchChildren).toHaveBeenNthCalledWith(1, { mediaID: "m1", ...show, level: 1 });
    expect(dialog.getByRole("img", { name: "Catalog show" })).toHaveAttribute("loading", "lazy");
    expect(dialog.getByRole("heading", { name: "Season" })).toBeInTheDocument();
    expect(dialog.getByRole("heading", { name: "Episode" })).toBeInTheDocument();
    await user.click(await dialog.findByRole("button", { name: row.parent }));
    expect(fixMatchChildren).toHaveBeenNthCalledWith(2, {
      mediaID: "m1",
      ...show,
      level: 2,
      parentKey: row.key,
    });
    if (row.key === "s2") {
      expect(await dialog.findByRole("img", { name: "Episode One" })).toHaveAttribute(
        "loading",
        "lazy",
      );
    }
    await user.click(await dialog.findByRole("button", { name: row.child }));
    const expected = {
      mediaID: "m1",
      ...show,
      sequence: row.sequence,
      index: row.index,
      clearLocks: ["ordinal"],
    };
    expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith(expected);
    expect(
      await dialog.findByText(
        `Season 1 · Episode 3 → Season ${row.sequence} · Episode ${row.index}`,
      ),
    ).toBeVisible();
    expect(fixMatch).not.toHaveBeenCalled();
    await user.click(dialog.getByRole("button", { name: "Apply" }));
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(expected, undefined);
  });

  it("previews a candidate without sending ordinals", async () => {
    const view = await open();
    await pickCandidate(view);
    await view.user.click(view.dialog.getByRole("button", { name: "Preview candidate" }));
    await view.dialog.findByText("A corrected overview.");
    await view.user.click(view.dialog.getByRole("button", { name: "Apply" }));
    expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith({
      mediaID: "m1",
      ...show,
      clearLocks: ["ordinal"],
    });
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(
      { mediaID: "m1", ...show, clearLocks: ["ordinal"] },
      undefined,
    );
  });

  it("reports loading, errors and empty results in their own column", async () => {
    let fail: (error: Error) => void = () => {};
    vi.mocked(fixMatchChildren)
      .mockImplementationOnce(
        () =>
          new Promise((_resolve, reject) => {
            fail = reject;
          }),
      )
      .mockResolvedValueOnce([]);
    const view = await open();
    await pickCandidate(view);
    expect(view.dialog.getByRole("status")).toHaveTextContent("Loading");
    await act(async () => fail(new Error("network")));
    expect(view.dialog.getByRole("alert")).toHaveTextContent(
      "Could not load entries at this level.",
    );
    await view.user.click(view.dialog.getByRole("button", { name: "Try again" }));
    expect(await view.dialog.findByText("No entries at this level.")).toBeVisible();
  });

  it("keeps the latest parent selection when child requests resolve out of order", async () => {
    let resume = () => {};
    vi.mocked(fixMatchChildren).mockImplementation(({ level, parentKey }) => {
      if (level === 1) return Promise.resolve([season, specials]);
      if (parentKey === "s0") {
        return Promise.resolve([{ key: "holiday", label: "Holiday", sequence: "0", index: 1 }]);
      }
      return new Promise((resolve) => {
        resume = () => resolve(episodes);
      });
    });
    const view = await open();
    const { user, dialog } = view;
    await pickCandidate(view);
    await user.click(await dialog.findByRole("button", { name: /Second season/ }));
    await user.click(dialog.getByRole("button", { name: "Back" }));
    await user.click(dialog.getByRole("button", { name: /Specials/ }));
    expect(await dialog.findByRole("button", { name: "Holiday" })).toBeVisible();
    await act(async () => resume());
    expect(dialog.getByRole("button", { name: "Holiday" })).toBeVisible();
    expect(dialog.queryByRole("button", { name: /Episode Five/ })).not.toBeInTheDocument();
  });

  it("validates manual numbers and uses the source's generic labels", async () => {
    vi.mocked(fixMatchSources).mockResolvedValue([
      { ...childSource, childLevels: ["release", "bonus-track"] },
    ]);
    const view = await open();
    const { user, dialog } = view;
    await pickCandidate(view);
    expect(dialog.getByRole("heading", { name: "Bonus track" })).toBeInTheDocument();
    await user.click(dialog.getByRole("button", { name: "Enter numbers instead" }));
    const sequence = dialog.getByRole("textbox", { name: "Release" });
    await user.type(sequence, "x".repeat(33));
    expect(dialog.getByRole("alert")).toHaveTextContent(
      "Enter a sequence with at most 32 characters.",
    );
    await user.clear(sequence);
    await user.type(sequence, "A-side");
    const number = dialog.getByRole("spinbutton", { name: "Bonus track" });
    await user.type(number, "-1");
    expect(dialog.getByRole("alert")).toHaveTextContent("Enter a whole number");
    const previewButton = dialog.getByRole("button", { name: "Preview changes" });
    expect(previewButton).toBeDisabled();
    expect(fixMatchPreview).not.toHaveBeenCalled();
    await user.clear(number);
    await user.type(number, "0");
    await user.click(previewButton);
    const expected = {
      mediaID: "m1",
      ...show,
      sequence: "A-side",
      index: 0,
      clearLocks: ["ordinal"],
    };
    expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith(expected);
    expect(await dialog.findByText("— → Release A-side · Bonus track 0")).toBeVisible();
    await user.click(dialog.getByRole("button", { name: "Apply" }));
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(expected, undefined);
  });

  it("retains an unnumbered child's sequence and requires a manual index before preview", async () => {
    const view = await open();
    const { user, dialog } = view;
    await pickCandidate(view);
    await user.click(await dialog.findByRole("button", { name: /Second season/ }));
    await user.click(await dialog.findByRole("button", { name: "Unnumbered Special" }));
    expect(dialog.getByText("Enter a number for Episode.")).toBeVisible();
    expect(dialog.getByRole("textbox", { name: "Season" })).toHaveValue("2");
    expect(dialog.getByRole("textbox", { name: "Season" })).toHaveAttribute("readonly");
    expect(dialog.getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(fixMatchPreview).not.toHaveBeenCalled();
    await user.type(dialog.getByRole("spinbutton", { name: "Episode" }), "7");
    await user.click(dialog.getByRole("button", { name: "Preview changes" }));
    expect(await dialog.findByText("— → Season 2 · Episode 7")).toBeVisible();
    const expected = { mediaID: "m1", ...show, sequence: "2", index: 7, clearLocks: ["ordinal"] };
    expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith(expected);
    await user.click(dialog.getByRole("button", { name: "Apply" }));
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(expected, undefined);
  });

  it.each([true, false])("defaults the ordinal lock by candidate ID (same: %s)", async (same) => {
    vi.mocked(fixMatchCurrent).mockResolvedValue({
      ...show,
      candidateID: same ? "show" : "other",
      title: "Old",
      year: 2022,
    });
    vi.mocked(fixMatchLockedFields).mockResolvedValue([{ field: "ordinal", value: "1 / 3" }]);
    const view = await open();
    const { user, dialog } = view;
    await pickCandidate(view);
    await user.click(dialog.getByRole("button", { name: "Preview candidate" }));
    const lock = await dialog.findByRole("checkbox", { name: /Clear operator ordinal/ });
    if (same) {
      expect(lock).not.toBeChecked();
    } else {
      expect(lock).toBeChecked();
      await user.click(lock);
    }
    await user.click(dialog.getByRole("button", { name: "Apply" }));
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(
      { mediaID: "m1", ...show, clearLocks: [] },
      undefined,
    );
  });

  it.each([true, false])(
    "previews the effective ordinal when replacing a lock (%s)",
    async (replace) => {
      vi.mocked(fixMatchCurrent).mockResolvedValue({ ...show, title: "Old", year: 2022 });
      vi.mocked(fixMatchLockedFields).mockResolvedValue([{ field: "ordinal", value: "1 / 3" }]);
      vi.mocked(fixMatchPreview).mockImplementation(async ({ index }) => [
        {
          field: "title",
          current: "Old",
          proposed: `Proposed title for ${index ?? 3}`,
          changed: true,
        },
      ]);
      const view = await open();
      const { user, dialog } = view;
      await pickCandidate(view);
      await user.click(await dialog.findByRole("button", { name: /Second season/ }));
      await user.click(await dialog.findByRole("button", { name: /Episode Five/ }));
      const lock = await dialog.findByRole("checkbox", { name: /Replace Ordinal/ });
      expect(lock).not.toBeChecked();
      expect(dialog.getByText("Proposed title for 3")).toBeVisible();
      expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith({
        mediaID: "m1",
        ...show,
        clearLocks: [],
      });
      if (replace) {
        await user.click(lock);
        expect(dialog.getByText("Proposed title for 5")).toBeVisible();
      }
      await user.click(dialog.getByRole("button", { name: "Apply" }));
      expect(fixMatch).toHaveBeenCalledExactlyOnceWith(
        {
          mediaID: "m1",
          ...show,
          ...(replace ? { clearLocks: ["ordinal"], sequence: "2", index: 5 } : { clearLocks: [] }),
        },
        undefined,
      );
    },
  );

  it("previews a same-candidate ordinal clear with the parsed ordinal Apply will use", async () => {
    vi.mocked(fixMatchCurrent).mockResolvedValue({ ...show, title: "Old", year: 2022 });
    vi.mocked(fixMatchLockedFields).mockResolvedValue([{ field: "ordinal", value: "1 / 3" }]);
    vi.mocked(fixMatchPreview).mockImplementation(async (vars) => [
      {
        field: "title",
        current: "Old",
        proposed: vars.clearLocks?.includes("ordinal")
          ? "Parsed episode title"
          : "Saved episode title",
        changed: true,
      },
    ]);
    const view = await open();
    const { user, dialog } = view;
    await pickCandidate(view);
    await user.click(dialog.getByRole("button", { name: "Preview candidate" }));
    const lock = await dialog.findByRole("checkbox", { name: /Clear operator ordinal/ });
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(dialog.getByText("Saved episode title")).toBeVisible();
    expect(apply).toBeEnabled();

    await user.click(lock);
    expect(await dialog.findByText("Parsed episode title")).toBeVisible();
    expect(dialog.queryByText("Saved episode title")).not.toBeInTheDocument();
    expect(fixMatchPreview).toHaveBeenLastCalledWith({
      mediaID: "m1",
      ...show,
      clearLocks: ["ordinal"],
    });
    expect(apply).toBeEnabled();

    await user.click(lock);
    expect(await dialog.findByText("Saved episode title")).toBeVisible();
    expect(apply).toBeEnabled();
  });

  it("keeps the replacement available when previewing the saved ordinal fails", async () => {
    vi.mocked(fixMatchCurrent).mockResolvedValue({
      ...show,
      candidateID: "old-show",
      title: "Old",
      year: 2022,
    });
    vi.mocked(fixMatchLockedFields).mockResolvedValue([{ field: "ordinal", value: "1 / 3" }]);
    let rejectKeep!: (error: Error) => void;
    vi.mocked(fixMatchPreview).mockImplementation(async ({ clearLocks }) => {
      if (clearLocks?.length === 0) {
        return new Promise((_resolve, reject) => {
          rejectKeep = reject;
        });
      }
      return [{ field: "title", current: "Old", proposed: "Replacement title", changed: true }];
    });
    const view = await open();
    const { user, dialog } = view;
    await pickCandidate(view);
    await user.click(await dialog.findByRole("button", { name: /Second season/ }));
    await user.click(await dialog.findByRole("button", { name: /Episode Five/ }));

    const lock = await dialog.findByRole("checkbox", { name: /Replace Ordinal/ });
    const apply = dialog.getByRole("button", { name: "Apply" });
    expect(lock).toBeChecked();
    expect(apply).toBeEnabled();
    expect(fixMatchPreview).toHaveBeenCalledExactlyOnceWith({
      mediaID: "m1",
      ...show,
      clearLocks: ["ordinal"],
      sequence: "2",
      index: 5,
    });

    await user.click(lock);
    expect(fixMatchPreview).toHaveBeenLastCalledWith({ mediaID: "m1", ...show, clearLocks: [] });
    expect(apply).toBeDisabled();
    expect(dialog.queryByText("Replacement title")).not.toBeInTheDocument();

    await user.click(lock);
    expect(dialog.getByText("Replacement title")).toBeVisible();
    expect(apply).toBeEnabled();
    await user.click(lock);
    expect(apply).toBeDisabled();
    expect(fixMatchPreview).toHaveBeenCalledTimes(2);

    await act(async () => rejectKeep(new Error("The replacement has no such ordinal")));
    expect(await dialog.findByRole("alert")).toHaveTextContent("Could not preview the candidate");
    expect(dialog.queryByText("Replacement title")).not.toBeInTheDocument();
    expect(apply).toBeDisabled();

    await user.click(lock);
    expect(dialog.queryByRole("alert")).not.toBeInTheDocument();
    expect(dialog.getByText("Replacement title")).toBeVisible();
    expect(apply).toBeEnabled();
    await user.click(apply);
    expect(fixMatch).toHaveBeenCalledExactlyOnceWith(
      { mediaID: "m1", ...show, clearLocks: ["ordinal"], sequence: "2", index: 5 },
      undefined,
    );
  });

  it("pre-pairs parsed and title indexes, refuses duplicates, and continues after a failure", async () => {
    const batch = [
      { mediaID: "m1", title: "File with an authoritative index S02E09", index: 1 },
      { mediaID: "m2", title: "File S02E02.mkv" },
      { mediaID: "m3", title: "File 03" },
    ];
    vi.mocked(fetchMedia).mockImplementation(async (id) => {
      const { mediaID, ...item } = batch.find(({ mediaID }) => mediaID === id)!;
      return { ...item, id: mediaID, favorited: false };
    });
    vi.mocked(fixMatch).mockImplementation(async ({ mediaID }) => {
      if (mediaID === "m2") throw new Error("Provider refused this item");
      return { id: mediaID, title: "Matched", favorited: false };
    });
    const view = await open(selection);
    const { user, dialog } = view;
    await pickCandidate(view);
    await user.click(await dialog.findByRole("button", { name: /Second season/ }));
    const table = within(await dialog.findByRole("table"));
    const [first, second, third] = table.getAllByRole("combobox");
    expect(first).toHaveValue("e1");
    expect(second).toHaveValue("e2");
    expect(third).toHaveValue("e3");

    const previewButton = dialog.getByRole("button", { name: "Preview changes" });
    const apply = dialog.getByRole("button", { name: "Apply" });
    await user.click(previewButton);
    await dialog.findByText("File 03", { selector: "h4" });
    expect(apply).toBeEnabled();
    await user.selectOptions(third, "e2");
    expect(await dialog.findByRole("alert")).toHaveTextContent(
      "Pair each catalog row with at most one item.",
    );
    expect(apply).toBeDisabled();
    expect(previewButton).toBeDisabled();
    await user.selectOptions(third, "");
    expect(table.getByText("Skipped")).toBeVisible();
    await user.selectOptions(third, "e5");
    expect(apply).toBeDisabled();
    await user.click(previewButton);
    await dialog.findByText("File 03", { selector: "h4" });
    await user.click(apply);

    expect(await table.findByText("Failed: Provider refused this item")).toBeVisible();
    expect(await table.findAllByText("Applied")).toHaveLength(2);
    const expected = [1, 2, 5].map((index, i) => ({
      mediaID: batch[i].mediaID,
      ...show,
      sequence: "2",
      index,
      clearLocks: ["ordinal"],
    }));
    expect(
      vi
        .mocked(fixMatchPreview)
        .mock.calls.slice(-3)
        .map(([vars]) => vars),
    ).toEqual(expected);
    expect(vi.mocked(fixMatch).mock.calls.map(([vars]) => vars)).toEqual(expected);
  });
});
