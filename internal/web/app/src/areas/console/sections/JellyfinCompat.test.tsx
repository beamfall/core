import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { fetchJellyfinCompat, setJellyfinCompat, setProfileIntegrationSignIn } from "@/lib/graphql";
import type { JellyfinCompatInfo } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { JellyfinCompat } from "./JellyfinCompat";

vi.mock("@/lib/graphql", () => ({
  fetchJellyfinCompat: vi.fn(),
  setJellyfinCompat: vi.fn(),
  setProfileIntegrationSignIn: vi.fn(),
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

beforeEach(() => {
  vi.mocked(fetchJellyfinCompat).mockReset().mockResolvedValue(settings);
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
});
