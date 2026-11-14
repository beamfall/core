import "@testing-library/jest-dom/vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { AvatarChooser } from "./AvatarChooser";
import { AVATAR_TONES, avatarToken, resolveAvatar } from "./avatar";

const profile = { id: "p1", name: "Robin" };

function deferred() {
  let resolve!: () => void;
  let reject!: (err: Error) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function tones() {
  const group = screen.getByRole("group", { name: "Colour" });
  return Array.from(group.querySelectorAll<HTMLInputElement>("input[type=radio]"));
}

function renderChooser(onSave: (token: string) => Promise<void>) {
  return render(
    <I18nProvider>
      <AvatarChooser profile={profile} onSave={onSave} />
    </I18nProvider>,
  );
}

describe("AvatarChooser", () => {
  it("saves one pick at a time and ends on the latest", async () => {
    const user = userEvent.setup();
    const first = deferred();
    const onSave = vi.fn<(token: string) => Promise<void>>().mockReturnValueOnce(first.promise);
    onSave.mockResolvedValue(undefined);
    renderChooser(onSave);
    const start = resolveAvatar(undefined, profile.id);
    const [a, b, c] = AVATAR_TONES.map((_, i) => i).filter((i) => i !== start.tone);

    await user.click(tones()[a]);
    await user.click(tones()[b]);
    await user.click(tones()[c]);
    expect(onSave).toHaveBeenCalledTimes(1);

    await act(async () => first.resolve());

    expect(onSave.mock.calls.map(([token]) => token)).toEqual([
      avatarToken({ ...start, tone: a }),
      avatarToken({ ...start, tone: c }),
    ]);
    expect(tones()[c]).toBeChecked();
  });

  it("keeps the newer pick when an older save fails late", async () => {
    const user = userEvent.setup();
    const first = deferred();
    const onSave = vi.fn<(token: string) => Promise<void>>().mockReturnValueOnce(first.promise);
    onSave.mockResolvedValue(undefined);
    renderChooser(onSave);
    const start = resolveAvatar(undefined, profile.id);
    const [a, b] = AVATAR_TONES.map((_, i) => i).filter((i) => i !== start.tone);

    await user.click(tones()[a]);
    await user.click(tones()[b]);
    await act(async () => first.reject(new Error("offline")));

    expect(onSave).toHaveBeenCalledTimes(2);
    expect(tones()[b]).toBeChecked();
  });

  it("drops a queued pick once the chooser has gone", async () => {
    const user = userEvent.setup();
    const first = deferred();
    const onSave = vi.fn<(token: string) => Promise<void>>().mockReturnValueOnce(first.promise);
    onSave.mockResolvedValue(undefined);
    const view = renderChooser(onSave);
    const start = resolveAvatar(undefined, profile.id);
    const [a, b] = AVATAR_TONES.map((_, i) => i).filter((i) => i !== start.tone);

    await user.click(tones()[a]);
    await user.click(tones()[b]);
    view.unmount();
    await act(async () => first.reject(new Error("aborted")));

    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it("drops a queued pick when the chooser moves to another Profile", async () => {
    const user = userEvent.setup();
    const first = deferred();
    const onSave = vi.fn<(token: string) => Promise<void>>().mockReturnValueOnce(first.promise);
    onSave.mockResolvedValue(undefined);
    const view = renderChooser(onSave);
    const start = resolveAvatar(undefined, profile.id);
    const [a, b] = AVATAR_TONES.map((_, i) => i).filter((i) => i !== start.tone);

    await user.click(tones()[a]);
    await user.click(tones()[b]);
    const other = { id: "p2", name: "Sam" };
    view.rerender(
      <I18nProvider>
        <AvatarChooser profile={other} onSave={onSave} />
      </I18nProvider>,
    );
    await act(async () => first.resolve());

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(tones()[resolveAvatar(undefined, other.id).tone]).toBeChecked();
  });

  it("puts the last saved avatar back when the latest save fails", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn<(token: string) => Promise<void>>().mockRejectedValue(new Error("no"));
    renderChooser(onSave);
    const start = resolveAvatar(undefined, profile.id);
    const other = start.tone === 0 ? 1 : 0;

    await user.click(tones()[other]);

    expect(tones()[start.tone]).toBeChecked();
  });
});
