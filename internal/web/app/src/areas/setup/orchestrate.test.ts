import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import {
  activatePlugins,
  bootstrapResume,
  bootstrapSetup,
  classifySetupError,
  configureUpdateCheck,
  createFirstLibrary,
  FirstLibraryError,
  SetupTokenError,
} from "./orchestrate";

// Request-recording tests for the first-run setup orchestration (WEB-1,
// ADR-0007 §2). We stub the global `fetch` (the single network boundary every
// api.ts call funnels through) and record every request the orchestration
// sends, then assert the exact on-wire shape: the /api/setup POST body, the
// 401 → invalid-token path, createLibrary's familyScope per tile +
// setLibraryRoots + scanLibrary, and setPluginActive. Stubbing fetch (not the
// graphql wrapper) means the recorded bodies are the actual operations +
// variables.

interface Recorded {
  url: string;
  method: string;
  body: Record<string, unknown>;
}

const recorded: Recorded[] = [];
// Per-test responder: maps a request to { status, json } for the response.
let respond: (rec: Recorded) => { status: number; json: unknown };

beforeEach(() => {
  recorded.length = 0;
  respond = () => ({ status: 200, json: { data: {} } });
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string, init?: RequestInit) => {
      const body = init?.body ? JSON.parse(init.body as string) : {};
      const rec: Recorded = {
        url,
        method: init?.method ?? "GET",
        body,
      };
      recorded.push(rec);
      const { status, json } = respond(rec);
      return Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(json),
      } as Response);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("bootstrapSetup", () => {
  it("posts to /api/setup with the token, admin password, and profile name", async () => {
    respond = () => ({
      status: 200,
      json: {
        deviceID: "dev1",
        sessionExpiresAt: "2026-06-16T00:00:00Z",
        profileID: "prof1",
        deviceRole: "manager",
      },
    });

    const result = await bootstrapSetup({
      token: "SETUP-TOKEN-123",
      deviceName: "Web Browser",
      profileName: "Owner",
      adminPassword: "hunter2",
    });

    expect(result.deviceRole).toBe("manager");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].url).toBe("/api/setup");
    expect(recorded[0].method).toBe("POST");
    expect(recorded[0].body).toEqual({
      token: "SETUP-TOKEN-123",
      deviceName: "Web Browser",
      profileName: "Owner",
      adminPassword: "hunter2",
    });
  });

  it("surfaces SetupTokenError on a 401 and records exactly one request", async () => {
    respond = () => ({ status: 401, json: {} });

    await expect(
      bootstrapSetup({
        token: "bad",
        deviceName: "Web Browser",
        profileName: "Owner",
        adminPassword: "hunter2",
      }),
    ).rejects.toBeInstanceOf(SetupTokenError);

    // Only the bootstrap was attempted; no library/plugin requests followed.
    expect(recorded).toHaveLength(1);
    expect(recorded[0].url).toBe("/api/setup");
  });
});

describe("createFirstLibrary", () => {
  it("calls createLibrary with the correct familyScope per tile, then setLibraryRoots and scanLibrary", async () => {
    respond = (rec) => {
      const q = String(rec.body.query ?? "");
      if (q.includes("CreateLibrary"))
        return {
          status: 200,
          json: {
            data: {
              createLibrary: { id: "lib1", title: "TV Shows", pluginID: "filmtv" },
            },
          },
        };
      if (q.includes("ScanLibrary"))
        return {
          status: 200,
          json: { data: { scanLibrary: { id: "job-scan-1", status: "queued" } } },
        };
      return {
        status: 200,
        json: { data: { setLibraryRoots: { id: "lib1", roots: ["/media/tv"] } } },
      };
    };

    const result = await createFirstLibrary({
      title: "TV Shows",
      type: "tvshows",
      rootPath: "/media/tv",
    });
    expect(result).toEqual({
      libraryID: "lib1",
      scanJobID: "job-scan-1",
      scanStatus: "queued",
    });

    // Three GraphQL requests in order: createLibrary, setLibraryRoots, scanLibrary.
    expect(recorded).toHaveLength(3);
    expect(recorded[0].url).toBe("/api/graphql");
    expect(String(recorded[0].body.query)).toContain("createLibrary");
    expect(recorded[0].body.variables).toMatchObject({
      title: "TV Shows",
      pluginID: "filmtv",
      familyScope: "tvshows",
    });

    expect(String(recorded[1].body.query)).toContain("setLibraryRoots");
    expect(recorded[1].body.variables).toMatchObject({
      libraryID: "lib1",
      roots: ["/media/tv"],
    });

    expect(String(recorded[2].body.query)).toContain("scanLibrary");
    expect(recorded[2].body.variables).toMatchObject({
      libraryID: "lib1",
      root: "/media/tv",
    });
  });

  it("maps Music to the music plugin with no familyScope", async () => {
    respond = (rec) => {
      const q = String(rec.body.query ?? "");
      if (q.includes("CreateLibrary"))
        return {
          status: 200,
          json: {
            data: {
              createLibrary: { id: "lib2", title: "Tunes", pluginID: "music" },
            },
          },
        };
      if (q.includes("ScanLibrary"))
        return {
          status: 200,
          json: { data: { scanLibrary: { id: "job-scan-2", status: "queued" } } },
        };
      return {
        status: 200,
        json: { data: { setLibraryRoots: { id: "lib2", roots: ["/media/music"] } } },
      };
    };

    await createFirstLibrary({ title: "Tunes", type: "music", rootPath: "/media/music" });
    const vars = recorded[0].body.variables as Record<string, unknown>;
    expect(vars.pluginID).toBe("music");
    expect(vars.familyScope).toBeUndefined();
  });

  it("retries roots and scan without creating a second Library after roots fail", async () => {
    let rootsAttempts = 0;
    respond = (rec) => {
      const q = String(rec.body.query ?? "");
      if (q.includes("CreateLibrary")) {
        return { status: 200, json: { data: { createLibrary: { id: "lib1" } } } };
      }
      if (q.includes("SetLibraryRoots") && rootsAttempts++ === 0) {
        return { status: 200, json: { errors: [{ message: "root is not readable" }] } };
      }
      if (q.includes("SetLibraryRoots")) {
        return { status: 200, json: { data: { setLibraryRoots: { id: "lib1" } } } };
      }
      return {
        status: 200,
        json: { data: { scanLibrary: { id: "scan1", status: "queued" } } },
      };
    };

    let progress = { rootsSet: false };
    try {
      await createFirstLibrary({ title: "Films", type: "films", rootPath: "/films", progress });
      expect.unreachable("roots failure should reject");
    } catch (err) {
      expect(err).toBeInstanceOf(FirstLibraryError);
      expect((err as FirstLibraryError).message).toBe("root is not readable");
      progress = (err as FirstLibraryError).progress;
    }

    await createFirstLibrary({ title: "Films", type: "films", rootPath: "/films", progress });
    expect(recorded.filter((rec) => String(rec.body.query).includes("CreateLibrary"))).toHaveLength(
      1,
    );
    expect(
      recorded.filter((rec) => String(rec.body.query).includes("SetLibraryRoots")),
    ).toHaveLength(2);
    expect(recorded.filter((rec) => String(rec.body.query).includes("ScanLibrary"))).toHaveLength(
      1,
    );
  });

  it("retries only the scan after scanLibrary fails", async () => {
    let scanAttempts = 0;
    respond = (rec) => {
      const q = String(rec.body.query ?? "");
      if (q.includes("CreateLibrary"))
        return { status: 200, json: { data: { createLibrary: { id: "lib1" } } } };
      if (q.includes("SetLibraryRoots"))
        return { status: 200, json: { data: { setLibraryRoots: { id: "lib1" } } } };
      if (scanAttempts++ === 0)
        return { status: 200, json: { errors: [{ message: "scan queue unavailable" }] } };
      return {
        status: 200,
        json: { data: { scanLibrary: { id: "scan1", status: "queued" } } },
      };
    };

    let progress = { rootsSet: false };
    try {
      await createFirstLibrary({ title: "Films", type: "films", rootPath: "/films", progress });
    } catch (err) {
      progress = (err as FirstLibraryError).progress;
    }
    await createFirstLibrary({ title: "Films", type: "films", rootPath: "/films", progress });

    expect(recorded.filter((rec) => String(rec.body.query).includes("CreateLibrary"))).toHaveLength(
      1,
    );
    expect(
      recorded.filter((rec) => String(rec.body.query).includes("SetLibraryRoots")),
    ).toHaveLength(1);
    expect(recorded.filter((rec) => String(rec.body.query).includes("ScanLibrary"))).toHaveLength(
      2,
    );
  });
});

describe("activatePlugins", () => {
  it("calls setPluginActive(active: true) for each chosen plugin", async () => {
    respond = () => ({
      status: 200,
      json: { data: { setPluginActive: { backgroundWorkers: 0, plugins: [] } } },
    });

    await activatePlugins(["filmtv", "music"]);

    expect(recorded).toHaveLength(2);
    expect(String(recorded[0].body.query)).toContain("setPluginActive");
    expect(recorded[0].body.variables).toMatchObject({ pluginID: "filmtv", active: true });
    expect(recorded[1].body.variables).toMatchObject({ pluginID: "music", active: true });
  });
});

describe("configureUpdateCheck", () => {
  it("opens an Admin Session, rebinds compatibility, then persists the update opt-in without telemetry data", async () => {
    respond = (rec) => {
      const q = String(rec.body.query ?? "");
      if (q.includes("VerifyAdminSession")) {
        return {
          status: 200,
          json: { data: { verifyAdminSession: { active: true } } },
        };
      }
      if (rec.url === "/api/compat/handshake") {
        return {
          status: 200,
          json: { decision: "compatible", messageClass: "compat-compatible" },
        };
      }
      return {
        status: 200,
        json: {
          data: {
            setConsoleConfig: {
              backgroundWorkers: 4,
              updateCheckEnabled: true,
              updateCheckChannel: "beta",
              plugins: [],
            },
          },
        },
      };
    };

    await configureUpdateCheck({
      adminPassword: "hunter2",
      enabled: true,
      channel: "beta",
    });

    expect(recorded).toHaveLength(3);
    expect(String(recorded[0].body.query)).toContain("verifyAdminSession");
    expect(recorded[0].body.variables).toEqual({ password: "hunter2" });
    expect(recorded[1].url).toBe("/api/compat/handshake");
    expect(recorded[1].body).toMatchObject({ surface: "core-web-ui" });
    expect(String(recorded[2].body.query)).toContain("setConsoleConfig");
    expect(recorded[2].body.variables).toEqual({
      updateCheckEnabled: true,
      updateCheckChannel: "beta",
    });
  });
});

describe("bootstrapResume", () => {
  it("reports a fresh server (token not yet consumed) without reading setupState", async () => {
    respond = () => ({ status: 200, json: { required: true } });

    const resume = await bootstrapResume();

    expect(resume).toEqual({ alreadyBootstrapped: false, state: null });
    // Only the probe ran; no setupState GraphQL query when setup is fresh.
    expect(recorded).toHaveLength(1);
    expect(recorded[0].url).toBe("/api/setup");
    expect(recorded[0].method).toBe("GET");
  });

  it("reads setupState when the token is already consumed (resume after restart)", async () => {
    respond = (rec) => {
      if (rec.url === "/api/setup") return { status: 200, json: { required: false } };
      return {
        status: 200,
        json: {
          data: {
            setupState: {
              adminPasswordSet: true,
              hasLibrary: false,
              hasOwnerProfile: true,
              pluginsActivated: false,
              complete: false,
            },
          },
        },
      };
    };

    const resume = await bootstrapResume();

    expect(resume.alreadyBootstrapped).toBe(true);
    expect(resume.state).toMatchObject({ hasLibrary: false, complete: false });
    expect(recorded[0].url).toBe("/api/setup");
    expect(String(recorded[1].body.query)).toContain("setupState");
  });

  it("treats an existing rootless Library as an incomplete Library step", async () => {
    respond = (rec) => {
      if (rec.url === "/api/setup") return { status: 200, json: { required: false } };
      const q = String(rec.body.query ?? "");
      if (q.includes("SetupState")) {
        return {
          status: 200,
          json: {
            data: {
              setupState: {
                adminPasswordSet: true,
                hasLibrary: true,
                hasOwnerProfile: true,
                pluginsActivated: true,
                complete: true,
              },
            },
          },
        };
      }
      return { status: 200, json: { data: { libraries: [{ id: "lib1", roots: [] }] } } };
    };

    const resume = await bootstrapResume();

    expect(resume.state).toMatchObject({ hasLibrary: false, complete: false });
    expect(resume.firstLibraryProgress).toEqual({ libraryID: "lib1", rootsSet: false });
    expect(recorded).toHaveLength(3);
  });

  it("treats a setupState failure (stale session) as bootstrapped with no state", async () => {
    respond = (rec) => {
      if (rec.url === "/api/setup") return { status: 200, json: { required: false } };
      // A 401 from the GraphQL query → fetchSetupState throws → recovered.
      return { status: 401, json: {} };
    };

    const resume = await bootstrapResume();

    expect(resume).toEqual({ alreadyBootstrapped: true, state: null });
  });
});

describe("classifySetupError", () => {
  it("maps any provisioning error to the generic setupFailed key (no raw text leak)", () => {
    expect(classifySetupError(new ApiError("permission denied: /media/films", 200))).toBe(
      "setupFailed",
    );
    expect(classifySetupError(new Error("ENOENT /etc/secret"))).toBe("setupFailed");
    expect(classifySetupError("unexpected")).toBe("setupFailed");
  });
});
