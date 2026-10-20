import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { addPathPickerRoot, fetchPathPicker } from "@/lib/graphql";
import type { PathPickerRole } from "@/lib/graphql/types";
import { I18nProvider } from "@/lib/i18n";
import { PathPickerField } from "./index";

vi.mock("@/lib/graphql", () => ({
  addPathPickerRoot: vi.fn(),
  fetchPathPicker: vi.fn(),
}));

const roles: PathPickerRole[] = [
  "libraryRoot",
  "incomingFolder",
  "downloadDestination",
  "backupDestination",
];

function picker(role: PathPickerRole) {
  return {
    runtime: "bare" as const,
    roles,
    role,
    purposeKey: `pathPicker.${role}`,
    previewTimeoutMs: 1000,
    maxPreviewEntries: 50,
    roots: [{ name: "Media", path: "/srv/media", entryClass: "ordinary" as const }],
    entries: [
      {
        name: "Picked",
        path: `/srv/${role}`,
        kind: "folder" as const,
        accessError: "none" as const,
        entryClass: "ordinary" as const,
        selectable: true,
        mediaCount: 0,
        previewTimeoutMs: 1000,
        maxPreviewEntries: 50,
        truncated: false,
      },
    ],
  };
}

function renderField(role: PathPickerRole, suppressAppStoreDownload = false) {
  function Controlled() {
    const [value, setValue] = useState("");
    return (
      <I18nProvider>
        <PathPickerField
          id={`field-${role}`}
          label={`${role} path`}
          role={role}
          value={value}
          onChange={setValue}
          suppressAppStoreDownload={suppressAppStoreDownload}
        />
      </I18nProvider>
    );
  }
  return render(<Controlled />);
}

describe("PathPickerField", () => {
  beforeEach(() => {
    vi.mocked(fetchPathPicker)
      .mockReset()
      .mockImplementation(({ role }) => Promise.resolve(picker(role)));
    vi.mocked(addPathPickerRoot)
      .mockReset()
      .mockImplementation((path) =>
        Promise.resolve({ name: "Typed", path, entryClass: "ordinary" }),
      );
    delete window.beamfallHost;
  });

  it.each(roles)("keeps typed %s paths and audits Add as Root", async (role) => {
    const user = userEvent.setup();
    renderField(role);

    await user.type(screen.getByLabelText(`${role} path`), `/typed/${role}`);
    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(screen.getByRole("button", { name: "Add as Root" }));

    expect(addPathPickerRoot).toHaveBeenCalledWith(`/typed/${role}`);
    expect(screen.getByLabelText(`${role} path`)).toHaveValue(`/typed/${role}`);
  });

  it.each(roles)("fills %s from a browsed server path", async (role) => {
    const user = userEvent.setup();
    renderField(role);

    await user.click(screen.getByRole("button", { name: "Browse" }));
    await user.click(await screen.findByRole("button", { name: /Picked/ }));

    await waitFor(() => {
      expect(screen.getByLabelText(`${role} path`)).toHaveValue(`/srv/${role}`);
    });
    expect(fetchPathPicker).toHaveBeenCalledWith(
      expect.objectContaining({ role, path: `/srv/${role}` }),
    );
  });

  it("suppresses download destinations in an App Store host before querying", () => {
    window.beamfallHost = {
      kind: "apple-manager",
      platform: "ipad",
      clientClass: "ipad-manager-app-store",
    };

    renderField("downloadDestination", true);

    expect(screen.queryByLabelText("downloadDestination path")).toBeNull();
    expect(fetchPathPicker).not.toHaveBeenCalled();
  });
});
