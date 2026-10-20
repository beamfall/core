import "@testing-library/jest-dom/vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import {
  createMediaFromUnmatched,
  fetchUnmatchedRecords,
  fetchUnmatchedSuppressions,
  resetUnmatchedSuppression,
  suppressUnmatched,
} from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { CreateMediaForm } from "./CreateMediaForm";
import { RecordInbox } from "./RecordInbox";

vi.mock("@/lib/graphql", () => ({
  fetchUnmatchedRecords: vi.fn(),
  fetchUnmatchedSuppressions: vi.fn(),
  createMediaFromUnmatched: vi.fn(),
  suppressUnmatched: vi.fn(),
  resetUnmatchedSuppression: vi.fn(),
}));

const unmatchedRecord = {
  id: "urec-1",
  origin: "scan",
  libraryID: "lib-1",
  sourceHandle: "/media/Family_Reel_1998.mp4",
  candidateID: "cand-9",
  candidateTitle: "Family Reel",
  confidence: 0.2,
  failureReason: "no-candidates",
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchUnmatchedRecords).mockResolvedValue([unmatchedRecord]);
  vi.mocked(fetchUnmatchedSuppressions).mockResolvedValue([]);
  vi.mocked(createMediaFromUnmatched).mockResolvedValue({
    id: "media-manual-1",
    title: "Lewis family reunion",
    favorited: false,
  });
  vi.mocked(suppressUnmatched).mockResolvedValue(true);
  vi.mocked(resetUnmatchedSuppression).mockResolvedValue(true);
});

afterEach(() => {
  setIdentityEpochSignal();
  vi.unstubAllGlobals();
});

function renderInbox() {
  return render(
    <I18nProvider>
      <RecordInbox libraryID="lib-1" />
    </I18nProvider>,
  );
}

async function openRecord(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: /Family Reel/ }));
}

describe("RecordInbox", () => {
  // METAPARITY-6 (ADR-0068): the hand-authored create path — required title,
  // optional year, and an explicit "no external ID" statement.
  it("creates a hand-authored Media with no external ID", async () => {
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    expect(screen.getByText("No external ID")).toBeVisible();
    await user.clear(screen.getByLabelText("Title · required"));
    await user.type(screen.getByLabelText("Title · required"), "Lewis family reunion");
    await user.type(screen.getByLabelText("Year · optional"), "1998");
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    expect(createMediaFromUnmatched).toHaveBeenCalledWith({
      recordID: "urec-1",
      libraryID: "lib-1",
      title: "Lewis family reunion",
      year: 1998,
    });
    await waitFor(() => expect(screen.getByText(/Lewis family reunion created/)).toBeVisible());
  });

  it("uploads and applies artwork while creating the Media", async () => {
    const upload = vi.fn().mockResolvedValue({ ok: true, status: 201 });
    vi.stubGlobal("fetch", upload);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);
    const artwork = new File(["jpeg"], "family-reunion.jpg", { type: "image/jpeg" });

    await user.clear(screen.getByLabelText("Title · required"));
    await user.type(screen.getByLabelText("Title · required"), "Lewis family reunion");
    await user.upload(screen.getByLabelText("Artwork · optional JPG or PNG"), artwork);
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));
    expect(createMediaFromUnmatched).toHaveBeenCalledWith({
      recordID: "urec-1",
      libraryID: "lib-1",
      title: "Lewis family reunion",
      year: undefined,
    });
    const [path, init] = upload.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/media/artwork/upload");
    expect(init).toMatchObject({
      method: "POST",
      credentials: "same-origin",
      headers: { "X-Beamfall-Csrf": "1" },
    });
    expect((init.body as FormData).get("file")).toBe(artwork);
    expect((init.body as FormData).get("mediaID")).toBe("media-manual-1");
    expect((init.body as FormData).get("slot")).toBe("poster");
    await waitFor(() => expect(screen.getByText(/Lewis family reunion created/)).toBeVisible());
  });

  it("discards an artwork response that arrives after an identity epoch change", async () => {
    let resolveUpload!: (response: { ok: boolean; status: number }) => void;
    const upload = vi.fn(
      (_path: string, init: RequestInit) =>
        new Promise<{ ok: boolean; status: number }>((resolve) => {
          resolveUpload = resolve;
          expect(init.signal).toBeInstanceOf(AbortSignal);
        }),
    );
    vi.stubGlobal("fetch", upload);
    const identity = new AbortController();
    setIdentityEpochSignal(identity.signal);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);
    const artwork = new File(["jpeg"], "family-reunion.jpg", { type: "image/jpeg" });

    await user.upload(screen.getByLabelText("Artwork · optional JPG or PNG"), artwork);
    await user.click(screen.getByRole("button", { name: "Create Media" }));
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));

    const signal = (upload.mock.calls[0]?.[1] as RequestInit).signal as AbortSignal;
    const aborted = vi.fn();
    signal.addEventListener("abort", aborted);
    identity.abort();
    setIdentityEpochSignal(new AbortController().signal);

    expect(signal.aborted).toBe(true);
    expect(aborted).toHaveBeenCalledOnce();
    await act(async () => resolveUpload({ ok: true, status: 201 }));

    expect(screen.queryByText(/Family Reel created/)).not.toBeInTheDocument();
    expect(screen.queryByText("Artwork could not be used")).not.toBeInTheDocument();
  });

  // The late-SUCCESS case above is only one of the four ways a create can land
  // in a departed generation. A rejection is the dangerous one: its handler
  // writes a VISIBLE error, so an unguarded prior-epoch AbortError puts the
  // departed identity's failure on the next identity's screen.
  it("discards an artwork rejection that arrives after an identity epoch change", async () => {
    let rejectUpload!: (reason: unknown) => void;
    const upload = vi.fn(
      () =>
        new Promise<never>((_resolve, reject) => {
          rejectUpload = reject;
        }),
    );
    vi.stubGlobal("fetch", upload);
    const identity = new AbortController();
    setIdentityEpochSignal(identity.signal);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    await user.upload(
      screen.getByLabelText("Artwork · optional JPG or PNG"),
      new File(["jpeg"], "family-reunion.jpg", { type: "image/jpeg" }),
    );
    await user.click(screen.getByRole("button", { name: "Create Media" }));
    await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));

    identity.abort();
    setIdentityEpochSignal(new AbortController().signal);
    await act(async () => {
      rejectUpload(new DOMException("The operation was aborted.", "AbortError"));
    });

    expect(screen.queryByText("Artwork could not be used")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Choose another file" })).not.toBeInTheDocument();
  });

  it("discards a save rejection that arrives after an identity epoch change", async () => {
    let rejectCreate!: (reason: unknown) => void;
    vi.mocked(createMediaFromUnmatched).mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          rejectCreate = reject;
        }),
    );
    const identity = new AbortController();
    setIdentityEpochSignal(identity.signal);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    await user.click(screen.getByRole("button", { name: "Create Media" }));
    await waitFor(() => expect(createMediaFromUnmatched).toHaveBeenCalledTimes(1));

    identity.abort();
    setIdentityEpochSignal(new AbortController().signal);
    await act(async () => {
      rejectCreate(new DOMException("The operation was aborted.", "AbortError"));
    });

    expect(screen.queryByText(/Beamfall could not save this Media/)).not.toBeInTheDocument();
  });

  // The artwork retry runs against a Media created by ONE identity. It captured
  // no generation at all before this ticket, so both its outcomes could land in
  // a departed epoch — and its success outcome calls onCreated, which resolves
  // the Unmatched record on whatever identity happens to be current.
  it("discards an artwork retry that lands after an identity epoch change", async () => {
    const rejectingUpload = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ error: "unsupported image" }), { status: 415 }),
      );
    vi.stubGlobal("fetch", rejectingUpload);
    const identity = new AbortController();
    setIdentityEpochSignal(identity.signal);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    await user.upload(
      screen.getByLabelText("Artwork · optional JPG or PNG"),
      new File(["not-image"], "family-reunion.jpg", { type: "image/jpeg" }),
    );
    await user.click(screen.getByRole("button", { name: "Create Media" }));
    expect(await screen.findByText("Artwork could not be used")).toBeVisible();

    let resolveRetry!: (response: Response) => void;
    const retryUpload = vi.fn(
      (_path: string, _init: RequestInit) =>
        new Promise<Response>((resolve) => {
          resolveRetry = resolve;
        }),
    );
    vi.stubGlobal("fetch", retryUpload);
    await user.upload(
      screen.getByLabelText("Artwork upload"),
      new File(["jpeg"], "second-try.jpg", { type: "image/jpeg" }),
    );
    await waitFor(() => expect(retryUpload).toHaveBeenCalledTimes(1));
    expect(retryUpload.mock.calls[0]?.[1].signal).toBe(identity.signal);

    identity.abort();
    setIdentityEpochSignal(new AbortController().signal);
    await act(async () => resolveRetry(new Response(null, { status: 201 })));

    // A prior-epoch retry resolves nothing: the record is not reported created.
    expect(screen.queryByText(/created · manual/)).not.toBeInTheDocument();
  });

  it("does not write back a create that completes after the form unmounts", async () => {
    let resolveCreate!: (media: { id: string; title: string; favorited: boolean }) => void;
    vi.mocked(createMediaFromUnmatched).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveCreate = resolve;
        }),
    );
    const onCreated = vi.fn();
    const view = render(
      <I18nProvider>
        <CreateMediaForm record={unmatchedRecord} libraryID="lib-1" onCreated={onCreated} />
      </I18nProvider>,
    );
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Create Media" }));
    await waitFor(() => expect(createMediaFromUnmatched).toHaveBeenCalledTimes(1));

    view.unmount();
    await act(async () => {
      resolveCreate({ id: "media-manual-1", title: "Family Reel", favorited: false });
    });

    expect(onCreated).not.toHaveBeenCalled();
  });

  it("keeps entered state and offers recovery when artwork is rejected", async () => {
    const upload = vi
      .fn()
      .mockResolvedValue(
        new Response(JSON.stringify({ error: "unsupported image" }), { status: 415 }),
      );
    vi.stubGlobal("fetch", upload);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);
    const artwork = new File(["not-image"], "family-reunion.jpg", { type: "image/jpeg" });

    await user.clear(screen.getByLabelText("Title · required"));
    await user.type(screen.getByLabelText("Title · required"), "Lewis family reunion");
    await user.type(screen.getByLabelText("Year · optional"), "1998");
    await user.upload(screen.getByLabelText("Artwork · optional JPG or PNG"), artwork);
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    expect(await screen.findByText("Artwork could not be used")).toBeVisible();
    expect(screen.getByText("Entered fields and the source file are unchanged.")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("unsupported image");
    expect(createMediaFromUnmatched).toHaveBeenCalledWith({
      recordID: "urec-1",
      libraryID: "lib-1",
      title: "Lewis family reunion",
      year: 1998,
    });
    const [, init] = upload.mock.calls[0] as [string, RequestInit];
    expect((init.body as FormData).get("file")).toBe(artwork);
    expect((init.body as FormData).get("mediaID")).toBe("media-manual-1");
    expect(screen.queryByText(/Lewis family reunion created/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Choose another file" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Continue without artwork" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Continue without artwork" }));
    await waitFor(() => expect(screen.getByText(/Lewis family reunion created/)).toBeVisible());
    expect(createMediaFromUnmatched).toHaveBeenCalledTimes(1);
  });

  // The mockup's `validation-error` state: invalid input writes NOTHING and the
  // entered values stay available for correction.
  it("blocks the create on an empty title or an out-of-range year", async () => {
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    await user.clear(screen.getByLabelText("Title · required"));
    await user.type(screen.getByLabelText("Year · optional"), "98");
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    expect(createMediaFromUnmatched).not.toHaveBeenCalled();
    expect(screen.getByText("Enter a title.")).toBeVisible();
    expect(
      screen.getByText("Use four digits between 1888 and the current year + 1."),
    ).toBeVisible();
    expect(screen.getByLabelText("Title · required")).toHaveAttribute("aria-invalid", "true");
    // The typed year survives the refusal.
    expect(screen.getByLabelText("Year · optional")).toHaveValue("98");
  });

  // METAPARITY-7 (ADR-0068 §A): the scope is named, defaults to the source
  // record, and the write addresses only that scope.
  it("names all three suppression scopes and defaults to the source record", async () => {
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    expect(screen.getByRole("radio", { name: /This source record/ })).toBeChecked();
    expect(screen.getByRole("radio", { name: /This file/ })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /This candidate/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Suppress" }));
    expect(suppressUnmatched).toHaveBeenCalledWith({
      scope: "source-record",
      recordID: "urec-1",
    });

    await user.click(screen.getByRole("radio", { name: /This candidate/ }));
    await user.click(screen.getByRole("button", { name: "Suppress" }));
    expect(suppressUnmatched).toHaveBeenLastCalledWith({
      scope: "candidate",
      recordID: "urec-1",
      candidateID: "cand-9",
    });
  });

  // Each active suppression carries its OWN reset, and that reset addresses only
  // its own scope — the sibling scopes are never touched.
  it("resets one scope without addressing its siblings", async () => {
    vi.mocked(fetchUnmatchedSuppressions).mockResolvedValue([
      {
        scope: "source-record",
        key: "urec-1",
        recordID: "urec-1",
        actorProfileID: "profile-default",
        createdAt: "2026-07-26T14:32:00Z",
      },
      {
        scope: "candidate",
        key: "urec-1\u0000cand-9",
        recordID: "urec-1",
        candidateID: "cand-9",
        createdAt: "2026-07-26T14:36:00Z",
      },
    ]);
    const user = userEvent.setup();
    renderInbox();
    await openRecord(user);

    // Provenance is rendered next to each reset.
    expect(await screen.findByText(/profile-default · 2026-07-26T14:32:00Z/)).toBeVisible();
    const resets = screen.getAllByRole("button", { name: "Reset suppression" });
    expect(resets).toHaveLength(2);

    await user.click(resets[1]);
    expect(resetUnmatchedSuppression).toHaveBeenCalledTimes(1);
    expect(resetUnmatchedSuppression).toHaveBeenCalledWith({
      scope: "candidate",
      recordID: "urec-1",
      fingerprint: undefined,
      candidateID: "cand-9",
    });
  });
});
