import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchJobs } from "@/lib/graphql";
import { I18nProvider } from "@/lib/i18n";
import { ManageArea } from ".";

// The locked state of the Manage area (CORE-1197), driven through the real
// GraphQL client so the server's in-band "admin session required" refusal is
// what opens the gate — the same wire shape a manager without an Admin Session
// receives.

vi.mock("@/components/chrome/AppShell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("@/lib/register", () => ({ useRegister: () => ({ setRegister: vi.fn() }) }));
vi.mock("@/lib/session", () => ({
  useSession: () => ({
    isManager: true,
    loading: false,
    needsEnroll: false,
    session: { deviceRole: "manager", profile: "adult", profileID: "p1" },
  }),
}));

const RAW_REFUSAL = "admin session required";

function jsonResponse(body: unknown): Response {
  return { ok: true, status: 200, json: async () => body } as Response;
}

/** A server whose Admin Session opens only for the given password. */
function installServer(adminPassword: string) {
  let admin = false;
  const requests: string[] = [];
  // Operations whose response is held back until the test releases it.
  const held = new Map<string, Promise<void>>();
  const server = {
    requests,
    /** The Admin Session ends without this tab hearing of it (locked elsewhere). */
    endAdminSession: () => {
      admin = false;
    },
    hold(operation: string) {
      let release = () => {};
      held.set(operation, new Promise<void>((resolve) => (release = resolve)));
      return release;
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/compat/handshake") {
        return jsonResponse({ decision: "compatible", messageClass: "compat-compatible" });
      }
      if (path !== "/api/graphql") throw new Error(`Unexpected request: ${path}`);
      const { query, variables } = JSON.parse(String(init?.body)) as {
        query: string;
        variables: Record<string, unknown>;
      };
      const operation = /^(?:query|mutation)\s+(\w+)/.exec(query)?.[1] ?? query;
      requests.push(operation);
      await held.get(operation);
      if (operation === "AdminSession") {
        return jsonResponse({ data: { adminSession: { active: admin } } });
      }
      if (operation === "VerifyAdminSession") {
        admin = variables.password === adminPassword;
        return jsonResponse({ data: { verifyAdminSession: { active: admin } } });
      }
      if (operation === "Tags") return jsonResponse({ data: { tags: [] } });
      if (!admin) return jsonResponse({ errors: [{ message: RAW_REFUSAL }] });
      if (operation === "Libraries") {
        return jsonResponse({
          data: { libraries: [{ id: "lib-1", title: "Feature Films", previewClips: false }] },
        });
      }
      if (operation === "Plugins") return jsonResponse({ data: { plugins: [] } });
      if (operation === "Backup") return jsonResponse({ data: { backup: { manifest: "m" } } });
      if (operation === "BackupHistory") return jsonResponse({ data: { backupHistory: [] } });
      throw new Error(`Unexpected GraphQL operation: ${operation}`);
    }),
  );
  return server;
}

function renderManage(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <I18nProvider initialLocale="en">
        <Routes>
          <Route path="/manage/*" element={<ManageArea />} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Manage without an Admin Session", () => {
  it("offers the admin unlock in place of the raw refusal and the empty state", async () => {
    installServer("correct horse");
    renderManage("/manage/libraries");

    expect(await screen.findByLabelText("Admin password")).toBeInTheDocument();
    expect(screen.getByText("Admin password needed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Unlock" })).toBeInTheDocument();
    expect(screen.queryByText(RAW_REFUSAL)).not.toBeInTheDocument();
    expect(screen.queryByText("Nothing to show in this library yet.")).not.toBeInTheDocument();
    // The Manage navigation stays, so the manager can still leave the page.
    expect(screen.getByRole("link", { name: "Collections" })).toBeInTheDocument();
  });

  it("loads the page's data in place once the admin password is accepted", async () => {
    const user = userEvent.setup();
    const { requests } = installServer("correct horse");
    renderManage("/manage/libraries");

    await user.type(await screen.findByLabelText("Admin password"), "wrong");
    await user.click(screen.getByRole("button", { name: "Unlock" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The admin password was not accepted.",
    );

    await user.type(screen.getByLabelText("Admin password"), "correct horse");
    await user.click(screen.getByRole("button", { name: "Unlock" }));

    expect(await screen.findByText("Feature Films")).toBeInTheDocument();
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(screen.queryByText(RAW_REFUSAL)).not.toBeInTheDocument();
    // The refused read is sent again after the unlock, not by a navigation.
    expect(requests.filter((operation) => operation === "Libraries")).toHaveLength(2);
  });

  it("does not gate a page the server serves to a manager", async () => {
    const { requests } = installServer("correct horse");
    renderManage("/manage/tags");

    await waitFor(() => expect(requests).toContain("Tags"));
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(requests).not.toContain("AdminSession");
  });

  it("does not gate the page when a background read opts out of the unlock", async () => {
    const { requests } = installServer("correct horse");
    renderManage("/manage/tags");
    await waitFor(() => expect(requests).toContain("Tags"));

    // The header's job count (CORE-1131) polls a read that needs an Admin Session.
    await expect(fetchJobs(undefined, { adminUnlock: false })).rejects.toThrow(RAW_REFUSAL);
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(requests).not.toContain("AdminSession");
  });

  it("keeps what the manager entered and finishes the refused action after the unlock", async () => {
    const user = userEvent.setup();
    const { requests } = installServer("correct horse");
    renderManage("/manage/backups");

    await user.type(await screen.findByLabelText("Destination"), "/backups/nightly");
    await user.click(screen.getByRole("button", { name: "Run backup" }));
    await user.type(await screen.findByLabelText("Admin password"), "correct horse");
    await user.click(screen.getByRole("button", { name: "Unlock" }));

    await waitFor(() => expect(requests).toContain("BackupHistory"));
    expect(screen.getByLabelText("Destination")).toHaveValue("/backups/nightly");
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(screen.queryByText("The backup could not be run.")).not.toBeInTheDocument();
    expect(requests.filter((operation) => operation === "Backup")).toHaveLength(2);
  });

  it("asks again when the Admin Session was ended elsewhere", async () => {
    const user = userEvent.setup();
    const server = installServer("correct horse");
    renderManage("/manage/backups");

    await user.type(await screen.findByLabelText("Destination"), "/backups/nightly");
    await user.click(screen.getByRole("button", { name: "Run backup" }));
    await user.type(await screen.findByLabelText("Admin password"), "correct horse");
    await user.click(screen.getByRole("button", { name: "Unlock" }));
    await waitFor(() => expect(server.requests).toContain("BackupHistory"));

    server.endAdminSession();
    await user.click(screen.getByRole("button", { name: "Load history" }));

    await user.type(await screen.findByLabelText("Admin password"), "correct horse");
    expect(screen.getByLabelText("Destination")).toHaveValue("/backups/nightly");
    await user.click(screen.getByRole("button", { name: "Unlock" }));
    // The read that was waiting completes; it is not dropped with the old session.
    expect(await screen.findByRole("button", { name: "Load history" })).toBeEnabled();
    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
  });

  it("does not gate the next page when a request from the page just left is refused late", async () => {
    const user = userEvent.setup();
    const server = installServer("correct horse");
    const releaseLibraries = server.hold("Libraries");
    const releasePlugins = server.hold("Plugins");
    renderManage("/manage/libraries");
    await waitFor(() => expect(server.requests).toContain("Libraries"));

    await user.click(screen.getByRole("link", { name: "Tags" }));
    await waitFor(() => expect(server.requests).toContain("Tags"));
    releaseLibraries();
    releasePlugins();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(screen.queryByLabelText("Admin password")).not.toBeInTheDocument();
    expect(server.requests).not.toContain("AdminSession");
  });
});
