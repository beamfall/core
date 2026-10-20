import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchRemoteAccess } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import {
  currentEndpointKind,
  isPublicCleartextConsoleLocation,
  RemoteAccessStatus,
} from "./RemoteAccess";

vi.mock("@/lib/graphql", () => ({
  fetchRemoteAccess: vi.fn(),
}));

beforeEach(() => {
  setRemoteAccess({
    directHTTPS: null,
    relay: null,
    plainHTTPPublicWarning: false,
    endpoints: [
      { kind: "lan", url: "http://beamfall.local" },
      { kind: "direct", url: "https://example.test" },
      { kind: "overlay", url: "https://overlay.test" },
      { kind: "self-hosted-relay", url: "https://relay.test" },
      { kind: "hosted", url: "https://hosted.test" },
    ],
  });
});

function setRemoteAccess(data: Awaited<ReturnType<typeof fetchRemoteAccess>>) {
  vi.mocked(fetchRemoteAccess).mockResolvedValue(data);
}

function renderStatus() {
  return render(
    <I18nProvider>
      <RemoteAccessStatus />
    </I18nProvider>,
  );
}

describe("RemoteAccessStatus", () => {
  it("surfaces connection classes from remote-access endpoints", async () => {
    renderStatus();

    expect(await screen.findByText("LAN (direct)")).toBeInTheDocument();
    expect(screen.getByText("Direct HTTPS (ACME)")).toBeInTheDocument();
    expect(screen.getByText("Overlay network")).toBeInTheDocument();
    expect(screen.getByText("Self-hosted relay")).toBeInTheDocument();
    expect(screen.getByText("Hosted relay")).toBeInTheDocument();
  });

  it("guides install and polling when no overlay is detected", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "lan", url: "http://beamfall.local" }],
    });

    renderStatus();

    expect(await screen.findByText("Not detected")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Install Tailscale" })).toHaveAttribute(
      "href",
      "https://tailscale.com/download",
    );
    expect(screen.getByRole("button", { name: "Poll for overlay" })).toBeInTheDocument();
  });

  it("shows the phone test URL when an overlay is present", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "overlay", url: "https://100.64.0.5:8096" }],
    });

    renderStatus();

    expect(await screen.findByText("Detected")).toBeInTheDocument();
    expect(screen.getByText("Phone test URL")).toBeInTheDocument();
    expect(screen.getByText("https://100.64.0.5:8096")).toBeInTheDocument();
  });

  it("marks overlay verified when the current host matches it", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: false,
      endpoints: [{ kind: "overlay", url: "https://localhost:3000" }],
    });

    renderStatus();

    expect(await screen.findByText("Verified")).toBeInTheDocument();
    expect(screen.getByText(/already reaching Beamfall through an overlay/)).toBeInTheDocument();
  });

  it("classifies the current endpoint kind by advertised host", () => {
    expect(
      currentEndpointKind([{ kind: "overlay", url: "https://100.64.0.5:8096" }], {
        host: "100.64.0.5:8096",
      }),
    ).toBe("overlay");
  });

  it("classifies public cleartext Console origins", () => {
    expect(isPublicCleartextConsoleLocation({ protocol: "http:", hostname: "203.0.113.10" })).toBe(
      true,
    );
    expect(isPublicCleartextConsoleLocation({ protocol: "https:", hostname: "203.0.113.10" })).toBe(
      false,
    );
    expect(isPublicCleartextConsoleLocation({ protocol: "http:", hostname: "192.168.1.20" })).toBe(
      false,
    );
  });

  it("shows the server cleartext warning", async () => {
    setRemoteAccess({
      directHTTPS: null,
      relay: null,
      plainHTTPPublicWarning: true,
      endpoints: [],
    });

    renderStatus();

    expect(
      await screen.findByText(
        /This Console is being reached over plain HTTP from a public address/,
      ),
    ).toBeInTheDocument();
  });
});
