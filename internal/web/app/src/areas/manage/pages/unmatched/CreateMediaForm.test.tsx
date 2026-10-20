import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMediaFromUnmatched } from "@/lib/graphql";
import type { UnmatchedRecordInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { CreateMediaForm } from "./CreateMediaForm";

vi.mock("@/lib/graphql", () => ({
  createMediaFromUnmatched: vi.fn(),
}));

const record: UnmatchedRecordInfo = {
  id: "urec-1",
  origin: "scan",
  libraryID: "lib-1",
  sourceHandle: "/media/Family_Reel_1998.mp4",
  candidateTitle: "Family Reel",
  confidence: 0.2,
  failureReason: "no-candidates",
};

function renderForm(onCreated = vi.fn()) {
  render(
    <I18nProvider>
      <CreateMediaForm record={record} libraryID="lib-1" onCreated={onCreated} />
    </I18nProvider>,
  );
  return onCreated;
}

async function fillTitle(user: ReturnType<typeof userEvent.setup>) {
  await user.clear(screen.getByLabelText("Title · required"));
  await user.type(screen.getByLabelText("Title · required"), "Lewis family reunion");
}

const artworkFile = new File(["fake-bytes"], "poster.jpg", { type: "image/jpeg" });

describe("CreateMediaForm artwork upload (METAPARITY-6-ARTWORK-1)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(createMediaFromUnmatched).mockResolvedValue({
      id: "media-manual-1",
      title: "Lewis family reunion",
      favorited: false,
    });
    vi.stubGlobal("fetch", vi.fn());
  });

  it("uploads the selected artwork to the created Media and completes", async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ uploaded: true }), { status: 200 }),
    );
    const user = userEvent.setup();
    const onCreated = renderForm();
    await fillTitle(user);
    await user.upload(screen.getByLabelText("Artwork upload"), artworkFile);
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("Lewis family reunion"));
    expect(fetch).toHaveBeenCalledWith(
      "/api/media/artwork/upload",
      expect.objectContaining({ method: "POST", credentials: "same-origin" }),
    );
    const [, init] = vi.mocked(fetch).mock.calls[0];
    const body = init?.body as FormData;
    expect(body.get("mediaID")).toBe("media-manual-1");
    expect(body.get("slot")).toBe("poster");
  });

  it("drops into artwork-error without discarding the created Media, then continues without artwork", async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ error: "unsupported image" }), { status: 400 }),
    );
    const user = userEvent.setup();
    const onCreated = renderForm();
    await fillTitle(user);
    await user.upload(screen.getByLabelText("Artwork upload"), artworkFile);
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    expect(await screen.findByText("Artwork could not be used")).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("unsupported image");
    expect(onCreated).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Continue without artwork" }));
    expect(onCreated).toHaveBeenCalledWith("Lewis family reunion");
  });

  it("retries a new file from the artwork-error state", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "unsupported image" }), { status: 400 }),
    );
    const user = userEvent.setup();
    const onCreated = renderForm();
    await fillTitle(user);
    await user.upload(screen.getByLabelText("Artwork upload"), artworkFile);
    await user.click(screen.getByRole("button", { name: "Create Media" }));
    expect(await screen.findByText("Artwork could not be used")).toBeVisible();

    vi.mocked(fetch).mockResolvedValueOnce(
      new Response(JSON.stringify({ uploaded: true }), { status: 200 }),
    );
    await user.upload(screen.getByLabelText("Artwork upload"), artworkFile);

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("Lewis family reunion"));
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("locks the artwork input while the create is in flight so the upload cannot go stale", async () => {
    let releaseCreate: (media: { id: string; title: string; favorited: boolean }) => void =
      () => {};
    vi.mocked(createMediaFromUnmatched).mockReturnValue(
      new Promise((resolve) => {
        releaseCreate = resolve;
      }),
    );
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ uploaded: true }), { status: 200 }),
    );
    const user = userEvent.setup();
    const onCreated = renderForm();
    await fillTitle(user);
    const input = screen.getByLabelText("Artwork upload") as HTMLInputElement;
    await user.upload(input, artworkFile);
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    // While suspended, the handler still holds the first render's file, so the
    // field must refuse a second selection rather than diverge from it.
    await waitFor(() => expect(input).toBeDisabled());
    const replacement = new File(["other-bytes"], "replacement.jpg", { type: "image/jpeg" });
    await user.upload(input, replacement);
    expect(input.files?.[0]?.name).toBe("poster.jpg");

    releaseCreate({ id: "media-manual-1", title: "Lewis family reunion", favorited: false });
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("Lewis family reunion"));
    const [, init] = vi.mocked(fetch).mock.calls[0];
    expect((init?.body as FormData).get("file")).toBe(artworkFile);
    expect(screen.getByLabelText("Artwork upload")).toBeEnabled();
  });

  it("blocks the create without ever attempting an artwork upload on invalid input", async () => {
    const user = userEvent.setup();
    renderForm();
    await user.clear(screen.getByLabelText("Title · required"));
    await user.upload(screen.getByLabelText("Artwork upload"), artworkFile);
    await user.click(screen.getByRole("button", { name: "Create Media" }));

    expect(createMediaFromUnmatched).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });
});
