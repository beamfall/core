import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  createJellyfinCompatKey,
  fetchJellyfinCompat,
  fetchJellyfinCompatKeys,
  revokeJellyfinCompatKey,
  setJellyfinCompat,
  setProfileIntegrationSignIn,
} from "@/lib/graphql";
import type { JellyfinCompatInfo, JellyfinCompatKeyInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { JellyfinCompat } from "./JellyfinCompat";

vi.mock("@/lib/graphql", () => ({
  fetchJellyfinCompat: vi.fn(),
  setJellyfinCompat: vi.fn(),
  setProfileIntegrationSignIn: vi.fn(),
  fetchJellyfinCompatKeys: vi.fn(),
  createJellyfinCompatKey: vi.fn(),
  revokeJellyfinCompatKey: vi.fn(),
}));

const settings: JellyfinCompatInfo = {
  enabled: false,
  allowedCIDRs: ["192.168.1.0/24"],
  trustedProxyCIDRs: [],
  libraries: [
    { id: "lib-films", title: "Films", allowed: true },
    { id: "lib-shows", title: "Shows", allowed: false },
  ],
  profiles: [{ id: "p-alex", name: "Alex", integrationSignIn: false }],
};

const seerrKey: JellyfinCompatKeyInfo = {
  id: "key-1",
  profileID: "p-alex",
  profileName: "Alex",
  name: "Seerr",
  scopes: ["compat.read", "compat.refresh"],
  createdAt: "2026-11-20T10:00:00Z",
  lastUsedAt: "2026-11-20T11:30:00Z",
};

const optedIn: JellyfinCompatInfo = {
  ...settings,
  profiles: [{ id: "p-alex", name: "Alex", integrationSignIn: true }],
};

beforeEach(() => {
  vi.mocked(fetchJellyfinCompat).mockReset().mockResolvedValue(settings);
  vi.mocked(fetchJellyfinCompatKeys).mockReset().mockResolvedValue([]);
  vi.mocked(createJellyfinCompatKey).mockReset();
  vi.mocked(revokeJellyfinCompatKey).mockReset();
  vi.mocked(setJellyfinCompat).mockReset();
  vi.mocked(setProfileIntegrationSignIn).mockReset();
});

function renderPage() {
  return render(
    <I18nProvider>
      <JellyfinCompat />
    </I18nProvider>,
  );
}

describe("JellyfinCompat", () => {
  it("explains the setup and shows the non-affiliation disclaimer", async () => {
    renderPage();

    expect(await screen.findByText(/not affiliated with or endorsed by Jellyfin/)).toBeVisible();
    expect(screen.getByText(/never through the relay or remote access/)).toBeVisible();
    expect(screen.getByText(/\/compat\/jellyfin\//)).toBeVisible();
    expect(screen.getByText(/sign in as admin with the admin password/)).toBeVisible();
    expect(screen.getByText(/must overwrite X-Forwarded-For/)).toBeVisible();
  });

  it("turns the API on and off through setJellyfinCompat", async () => {
    const user = userEvent.setup();
    vi.mocked(setJellyfinCompat).mockResolvedValueOnce({ ...settings, enabled: true });
    renderPage();

    const toggle = await screen.findByRole("switch", { name: "Compatibility API" });
    expect(toggle).not.toBeChecked();
    await user.click(toggle);

    expect(setJellyfinCompat).toHaveBeenCalledWith({ enabled: true });
    await waitFor(() => expect(toggle).toBeChecked());

    vi.mocked(setJellyfinCompat).mockResolvedValueOnce({ ...settings, enabled: false });
    await user.click(toggle);
    expect(setJellyfinCompat).toHaveBeenLastCalledWith({ enabled: false });
    await waitFor(() => expect(toggle).not.toBeChecked());
  });

  it("saves both network lists and shows the server's normalized form", async () => {
    const user = userEvent.setup();
    vi.mocked(setJellyfinCompat).mockResolvedValueOnce({
      ...settings,
      allowedCIDRs: ["10.0.0.0/8", "192.168.1.0/24"],
      trustedProxyCIDRs: ["127.0.0.1/32"],
    });
    renderPage();

    const allowed = await screen.findByLabelText("Allowed networks");
    expect(allowed).toHaveValue("192.168.1.0/24");
    await user.clear(allowed);
    await user.type(allowed, "10.0.0.1/8{enter}192.168.1.0/24");
    await user.type(screen.getByLabelText("Trusted proxy networks"), "127.0.0.1");
    await user.click(screen.getByRole("button", { name: "Save networks" }));

    expect(setJellyfinCompat).toHaveBeenCalledWith({
      allowedCIDRs: ["10.0.0.1/8", "192.168.1.0/24"],
      trustedProxyCIDRs: ["127.0.0.1"],
    });
    expect(await screen.findByText("Saved.")).toBeVisible();
    expect(allowed).toHaveValue("10.0.0.0/8\n192.168.1.0/24");
    expect(screen.getByLabelText("Trusted proxy networks")).toHaveValue("127.0.0.1/32");
  });

  it("shows the server's refusal when a network is outside the local ranges", async () => {
    const user = userEvent.setup();
    vi.mocked(setJellyfinCompat).mockRejectedValueOnce(
      new ApiError("allowedCIDRs: 100.64.0.0/10 is not a local network range", 200),
    );
    renderPage();

    const allowed = await screen.findByLabelText("Allowed networks");
    await user.clear(allowed);
    await user.type(allowed, "100.64.0.0/10");
    await user.click(screen.getByRole("button", { name: "Save networks" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "100.64.0.0/10 is not a local network range",
    );
    expect(screen.queryByText("Saved.")).toBeNull();
    // The rejected entry stays in the field to correct.
    expect(allowed).toHaveValue("100.64.0.0/10");
  });

  it("sends the whole library allowlist when a library is ticked or cleared", async () => {
    const user = userEvent.setup();
    vi.mocked(setJellyfinCompat).mockResolvedValueOnce({
      ...settings,
      libraries: settings.libraries.map((l) => ({ ...l, allowed: true })),
    });
    renderPage();

    await user.click(await screen.findByRole("checkbox", { name: "Shows" }));
    expect(setJellyfinCompat).toHaveBeenCalledWith({ libraryIDs: ["lib-films", "lib-shows"] });
    await waitFor(() => expect(screen.getByRole("checkbox", { name: "Shows" })).toBeChecked());

    vi.mocked(setJellyfinCompat).mockResolvedValueOnce({
      ...settings,
      libraries: [
        { id: "lib-films", title: "Films", allowed: false },
        { id: "lib-shows", title: "Shows", allowed: true },
      ],
    });
    await user.click(screen.getByRole("checkbox", { name: "Films" }));
    expect(setJellyfinCompat).toHaveBeenLastCalledWith({ libraryIDs: ["lib-shows"] });
  });

  it("holds every control while a save is in flight", async () => {
    const user = userEvent.setup();
    let finish: (value: typeof settings) => void = () => {};
    vi.mocked(setJellyfinCompat).mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    renderPage();

    await user.click(await screen.findByRole("checkbox", { name: "Shows" }));
    expect(screen.getByRole("checkbox", { name: "Films" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Compatibility API" })).toBeDisabled();
    // A save response replaces both network lists, so they cannot take edits meanwhile.
    expect(document.getElementById("jellyfin-compat-allowed")).toHaveAttribute("readonly");
    expect(document.getElementById("jellyfin-compat-proxies")).toHaveAttribute("readonly");
    finish({ ...settings, libraries: settings.libraries.map((l) => ({ ...l, allowed: true })) });
    await waitFor(() => expect(screen.getByRole("checkbox", { name: "Films" })).toBeEnabled());
    expect(document.getElementById("jellyfin-compat-allowed")).not.toHaveAttribute("readonly");
    expect(setJellyfinCompat).toHaveBeenCalledTimes(1);
  });

  it("opts a profile in through setProfileIntegrationSignIn and says what off does", async () => {
    const user = userEvent.setup();
    vi.mocked(setProfileIntegrationSignIn).mockResolvedValueOnce({
      id: "p-alex",
      name: "Alex",
      integrationSignIn: true,
    });
    renderPage();

    expect(
      await screen.findByText(/signs it out of every compatibility tool at once/),
    ).toBeVisible();
    const alex = screen.getByRole("switch", { name: "Alex" });
    await user.click(alex);

    expect(setProfileIntegrationSignIn).toHaveBeenCalledWith("p-alex", true);
    expect(setJellyfinCompat).not.toHaveBeenCalled();
    await waitFor(() => expect(alex).toBeChecked());
  });

  it("offers no key form until a profile is turned on", async () => {
    renderPage();

    expect(
      await screen.findByText("Turn on a profile above before you create a key."),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: "Create key" })).toBeNull();
    expect(await screen.findByText("No integration keys yet.")).toBeVisible();
  });

  it("mints a key for an opted-in profile and shows its token once", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchJellyfinCompat).mockResolvedValue(optedIn);
    vi.mocked(createJellyfinCompatKey).mockResolvedValueOnce({
      token: `bfj_${"a".repeat(64)}`,
      key: { ...seerrKey, lastUsedAt: undefined },
    });
    renderPage();

    const create = await screen.findByRole("button", { name: "Create key" });
    expect(create).toBeDisabled();
    expect(screen.getByLabelText("Profile")).toHaveValue("p-alex");
    vi.mocked(fetchJellyfinCompatKeys).mockResolvedValue([{ ...seerrKey, lastUsedAt: undefined }]);
    await user.type(screen.getByLabelText("Key name"), " Seerr ");
    await user.click(create);

    expect(createJellyfinCompatKey).toHaveBeenCalledWith("p-alex", "Seerr");
    expect(await screen.findByText(`bfj_${"a".repeat(64)}`)).toBeVisible();
    expect(screen.getByText(/never be shown again/)).toBeVisible();
    const row = (await screen.findByRole("cell", { name: "Seerr" })).closest("tr");
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText("Never")).toBeVisible();
    expect(screen.getByLabelText("Key name")).toHaveValue("");
  });

  it("mints one key however often the form is submitted while the mint is in flight", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchJellyfinCompat).mockResolvedValue(optedIn);
    let finish: (value: Awaited<ReturnType<typeof createJellyfinCompatKey>>) => void = () => {};
    vi.mocked(createJellyfinCompatKey).mockReturnValueOnce(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    renderPage();

    const input = await screen.findByLabelText("Key name");
    await user.type(input, "Seerr");
    // Two submits inside one act, before React commits the disabled button:
    // only the in-flight ref can refuse the second.
    const form = input.closest("form");
    if (!form) throw new Error("key name input has no form");
    act(() => {
      fireEvent.submit(form);
      fireEvent.submit(form);
    });
    expect(createJellyfinCompatKey).toHaveBeenCalledTimes(1);
    await user.type(input, "{Enter}");
    expect(screen.getByRole("button", { name: "Create key" })).toBeDisabled();
    finish({ token: `bfj_${"b".repeat(64)}`, key: { ...seerrKey, lastUsedAt: undefined } });
    expect(await screen.findByText(`bfj_${"b".repeat(64)}`)).toBeVisible();
    expect(createJellyfinCompatKey).toHaveBeenCalledTimes(1);
  });

  it("shows the server's refusal when a key cannot be minted", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchJellyfinCompat).mockResolvedValue(optedIn);
    vi.mocked(createJellyfinCompatKey).mockRejectedValueOnce(
      new ApiError("this profile already holds the most integration keys allowed", 200),
    );
    renderPage();

    await user.type(await screen.findByLabelText("Key name"), "Radarr");
    await user.click(screen.getByRole("button", { name: "Create key" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Couldn't create the key: this profile already holds the most integration keys allowed",
    );
    expect(screen.queryByText(/never be shown again/)).toBeNull();
  });

  it("lists keys with last use and revokes one behind a confirm", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchJellyfinCompat).mockResolvedValue(optedIn);
    vi.mocked(fetchJellyfinCompatKeys).mockResolvedValue([seerrKey]);
    vi.mocked(revokeJellyfinCompatKey).mockResolvedValueOnce({
      ...seerrKey,
      revokedAt: "2026-11-20T12:00:00Z",
    });
    renderPage();

    expect(await screen.findByText("2026-11-20T11:30:00Z")).toBeVisible();
    vi.mocked(fetchJellyfinCompatKeys).mockResolvedValue([
      { ...seerrKey, revokedAt: "2026-11-20T12:00:00Z" },
    ]);
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("The tool using it stops working at once.");
    await user.click(within(dialog).getByRole("button", { name: "Revoke" }));

    expect(revokeJellyfinCompatKey).toHaveBeenCalledWith("key-1");
    expect(await screen.findByText("Revoked")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Revoke" })).toBeNull();
  });

  it("reloads the key list when a profile is turned off", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchJellyfinCompat).mockResolvedValue(optedIn);
    vi.mocked(fetchJellyfinCompatKeys).mockResolvedValue([seerrKey]);
    vi.mocked(setProfileIntegrationSignIn).mockResolvedValueOnce({
      id: "p-alex",
      name: "Alex",
      integrationSignIn: false,
    });
    renderPage();

    await screen.findByText("2026-11-20T11:30:00Z");
    expect(fetchJellyfinCompatKeys).toHaveBeenCalledTimes(1);
    vi.mocked(fetchJellyfinCompatKeys).mockResolvedValue([
      { ...seerrKey, revokedAt: "2026-11-20T12:00:00Z" },
    ]);
    await user.click(screen.getByRole("switch", { name: "Alex" }));

    expect(await screen.findByText("Revoked")).toBeVisible();
    expect(fetchJellyfinCompatKeys).toHaveBeenCalledTimes(2);
    expect(screen.getByText("Turn on a profile above before you create a key.")).toBeVisible();
  });
});
