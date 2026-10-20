import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setAdminEpochSignal, withAdminEpoch } from "@/areas/console/adminEpoch";
import {
  cancelJob,
  createProfile,
  createRetentionRule,
  deleteRetentionRule,
  disableProfile,
  evictPlaybackRenditionCache,
  fetchAccessSchedule,
  fetchActivityLog,
  fetchAPIKeys,
  fetchAPITokens,
  fetchCapabilityStates,
  fetchConsoleConfig,
  fetchDevices,
  fetchEnforcedFilters,
  fetchLiveSessions,
  fetchPlaybackCache,
  fetchProfilesAdmin,
  fetchQBittorrentIngest,
  fetchRemoteAccess,
  fetchRetentionRules,
  fetchTranscodeConfig,
  fetchUpdateStatus,
  precomputePlaybackRenditions,
  retryJob,
  revokeAllDevices,
  revokeDevice,
  runEventsCompaction,
  setDeviceRole,
  setExtensionPreauthorization,
  setProfileLibraries,
  setProfileStreamCap,
  setRetentionCandidateHold,
  updateProfile,
  updateRetentionRule,
} from "./console";

interface Recorded {
  query: string;
  variables: Record<string, unknown>;
}

const recorded: Recorded[] = [];
const recordedSignals: (AbortSignal | null | undefined)[] = [];

// A single permissive data payload: each function dereferences exactly one
// top-level field, so supplying all of them keeps the graphql() helper from
// throwing on a missing `data.<field>` while these tests assert only the
// outgoing request (operation name + variables) at the choke-point.
const DATA = {
  remoteAccess: { directHTTPS: null, relay: null, plainHTTPPublicWarning: false, endpoints: [] },
  jobCancel: { id: "job-1", status: "cancelling" },
  jobRetry: { id: "job-1", status: "queued" },
  runEventsCompaction: { id: "job-2", kind: "events-compaction", status: "queued" },
  revokeDevice: { deviceID: "dev-1", transcodesAborted: 0 },
  setDeviceRole: { deviceID: "dev-1", name: "TV", role: "manager", revoked: false, current: false },
  revokeAllDevices: { revokedCount: 0, transcodesAborted: 0 },
  precomputePlaybackRenditions: { affected: 1 },
  evictPlaybackRenditionCache: { affected: 0 },
  createProfile: { id: "p-1", name: "Kids", kind: "managed", libraryIDs: ["library-local"] },
  updateProfile: {
    id: "p-1",
    disabled: true,
    maxConcurrentStreams: 2,
    libraryIDs: ["library-local"],
  },
  setProfileLibraries: { id: "p-1", libraryIDs: ["library-local"] },
  setExtensionPreauthorization: { profileID: "p-1", enabled: true },
  createRetentionRule: { id: "r-1", name: "Rule" },
  updateRetentionRule: { id: "r-1", name: "Rule" },
  deleteRetentionRule: { id: "r-1", name: "Rule" },
  setRetentionCandidateHold: { ruleID: "r-1", mediaID: "m-1", hold: true },
};

// The transport matrix: every Console READ the common fetch path
// (areas/console/useAsync) drives. Both transport assertions below run the
// whole matrix, so a reader that takes no signal fails on both counts.
const CONSOLE_READS: [string, (signal: AbortSignal) => Promise<unknown>][] = [
  ["fetchRemoteAccess", (s) => fetchRemoteAccess(s)],
  ["fetchConsoleConfig", (s) => fetchConsoleConfig(s)],
  ["fetchTranscodeConfig", (s) => fetchTranscodeConfig(s)],
  ["fetchLiveSessions", (s) => fetchLiveSessions(s)],
  ["fetchDevices", (s) => fetchDevices(s)],
  ["fetchPlaybackCache", (s) => fetchPlaybackCache(s)],
  ["fetchCapabilityStates", (s) => fetchCapabilityStates(s)],
  ["fetchAccessSchedule", (s) => fetchAccessSchedule("p-1", s)],
  ["fetchEnforcedFilters", (s) => fetchEnforcedFilters(s)],
  ["fetchQBittorrentIngest", (s) => fetchQBittorrentIngest(s)],
  ["fetchAPITokens", (s) => fetchAPITokens(s)],
  ["fetchAPIKeys", (s) => fetchAPIKeys(s)],
  ["fetchActivityLog", (s) => fetchActivityLog(s)],
  ["fetchRetentionRules", (s) => fetchRetentionRules(s)],
  ["fetchUpdateStatus", (s) => fetchUpdateStatus(s)],
  ["fetchProfilesAdmin", (s) => fetchProfilesAdmin(s)],
];

beforeEach(() => {
  recorded.length = 0;
  recordedSignals.length = 0;
  setAdminEpochSignal();
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, init?: RequestInit) => {
      recordedSignals.push(init?.signal);
      const body = init?.body
        ? (JSON.parse(init.body as string) as Recorded)
        : { query: "", variables: {} };
      recorded.push(body);
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data: DATA }),
      } as Response);
    }),
  );
});

afterEach(() => {
  setAdminEpochSignal();
  vi.unstubAllGlobals();
});

describe("console GraphQL requests", () => {
  it("fetchRemoteAccess requests endpoint connection classes", async () => {
    await fetchRemoteAccess();
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("query RemoteAccess");
    expect(recorded[0].query).toContain("plainHTTPPublicWarning");
    expect(recorded[0].query).toContain("endpoints { kind url }");
    expect(recorded[0].variables).toEqual({});
  });

  // Every Console READ the common fetch path (areas/console/useAsync) drives
  // must carry the caller's AbortSignal all the way to the transport. A fetcher
  // that accepts no signal, or accepts one and drops it, leaves the request
  // running after the Admin Session that authorized it has ended — the response
  // is dropped, but the work is not.
  it("threads the caller's AbortSignal to the transport for every Console read", async () => {
    for (const [name, read] of CONSOLE_READS) {
      recordedSignals.length = 0;
      const controller = new AbortController();
      await read(controller.signal);
      expect(recordedSignals, name).toHaveLength(1);
      expect(recordedSignals[0], name).toBeInstanceOf(AbortSignal);
      expect(recordedSignals[0], name).not.toBe(controller.signal);
      expect(recordedSignals[0]?.aborted, name).toBe(false);
      controller.abort();
      expect(recordedSignals[0]?.aborted, name).toBe(true);
    }
  });

  // The request-side half of the same contract. useAsync composes the caller's
  // own signal with the Admin-Session epoch (areas/console/adminEpoch) before
  // handing it to the fetcher, so the object the transport receives must be the
  // COMPOSED signal — one that reports aborted once the Admin Session expires
  // or the operator locks. A reader that drops the argument records no signal
  // at all here, so the in-flight request would outlive the Admin Session.
  it("aborts the in-flight request of every Console read when the Admin epoch ends", async () => {
    for (const [name, read] of CONSOLE_READS) {
      const adminEpoch = new AbortController();
      setAdminEpochSignal(adminEpoch.signal);
      const own = new AbortController();
      const composed = withAdminEpoch(own.signal);

      recordedSignals.length = 0;
      await read(composed);
      const sent = recordedSignals[0];
      expect(recordedSignals, name).toHaveLength(1);
      expect(sent, name).toBeInstanceOf(AbortSignal);
      expect(sent?.aborted, name).toBe(false);

      adminEpoch.abort();
      expect(sent?.aborted, name).toBe(true);
    }
  });

  it("cancelJob sends JobCancel(id)", async () => {
    // feature:jobs-live
    await cancelJob("job-1");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation JobCancel");
    expect(recorded[0].query).toContain("jobCancel(id: $id)");
    expect(recorded[0].variables).toEqual({ id: "job-1" });
  });

  it("retryJob sends JobRetry(id)", async () => {
    // feature:jobs-live
    await retryJob("job-1");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation JobRetry");
    expect(recorded[0].query).toContain("jobRetry(id: $id)");
    expect(recorded[0].variables).toEqual({ id: "job-1" });
  });

  it("runEventsCompaction sends RunEventsCompaction with no variables", async () => {
    // feature:events-compaction
    await runEventsCompaction();
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation RunEventsCompaction");
    expect(recorded[0].query).toContain("runEventsCompaction");
    expect(recorded[0].variables).toEqual({});
  });

  it("revokeDevice sends RevokeDevice(deviceID)", async () => {
    // feature:device-management
    await revokeDevice("dev-1");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation RevokeDevice");
    expect(recorded[0].query).toContain("revokeDevice(deviceID: $deviceID)");
    expect(recorded[0].variables).toEqual({ deviceID: "dev-1" });
  });

  it("revokeAllDevices sends RevokeAllDevices with no variables", async () => {
    // feature:device-management
    await revokeAllDevices();
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation RevokeAllDevices");
    expect(recorded[0].query).toContain("revokeAllDevices");
    expect(recorded[0].variables).toEqual({});
  });

  it("setDeviceRole sends SetDeviceRole(deviceID, role)", async () => {
    // feature:device-management
    await setDeviceRole("dev-1", "manager");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation SetDeviceRole");
    expect(recorded[0].query).toContain("setDeviceRole(deviceID: $deviceID, role: $role)");
    expect(recorded[0].variables).toEqual({ deviceID: "dev-1", role: "manager" });
  });

  it("precomputePlaybackRenditions sends PrecomputePlaybackRenditions(mediaID)", async () => {
    // feature:playback-rendition-cache
    await precomputePlaybackRenditions("media-1");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation PrecomputePlaybackRenditions");
    expect(recorded[0].query).toContain("precomputePlaybackRenditions(mediaID: $mediaID)");
    expect(recorded[0].variables).toEqual({ mediaID: "media-1" });
  });

  it("evictPlaybackRenditionCache sends EvictPlaybackRenditionCache with no variables", async () => {
    // feature:playback-rendition-cache
    await evictPlaybackRenditionCache();
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation EvictPlaybackRenditionCache");
    expect(recorded[0].query).toContain("evictPlaybackRenditionCache");
    expect(recorded[0].variables).toEqual({});
  });

  it("createProfile sends CreateProfile(name, kind)", async () => {
    // feature:profile-admin
    await createProfile("Kids", "managed");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation CreateProfile");
    expect(recorded[0].query).toContain("createProfile(name: $name, kind: $kind)");
    expect(recorded[0].variables).toEqual({ name: "Kids", kind: "managed" });
  });

  it("disableProfile sends UpdateProfile(profileID, disabled: true)", async () => {
    // feature:profile-admin
    await disableProfile("p-1");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation UpdateProfile");
    expect(recorded[0].query).toContain(
      "updateProfile(profileID: $profileID, disabled: $disabled)",
    );
    expect(recorded[0].variables).toEqual({ profileID: "p-1", disabled: true });
  });

  it("updateProfile sends UpdateProfile(profileID, name, kind)", async () => {
    // feature:profile-admin
    await updateProfile("p-1", "Kids", "managed");
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation UpdateProfile");
    expect(recorded[0].query).toContain(
      "updateProfile(profileID: $profileID, name: $name, kind: $kind)",
    );
    expect(recorded[0].variables).toEqual({
      profileID: "p-1",
      name: "Kids",
      kind: "managed",
    });
  });

  it("setProfileLibraries sends SetProfileLibraries(profileID, libraryIDs)", async () => {
    // feature:profile-admin
    await setProfileLibraries("p-1", ["library-local"]);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation SetProfileLibraries");
    expect(recorded[0].query).toContain(
      "setProfileLibraries(profileID: $profileID, libraryIDs: $libraryIDs)",
    );
    expect(recorded[0].variables).toEqual({
      profileID: "p-1",
      libraryIDs: ["library-local"],
    });
  });

  it("setProfileStreamCap sends UpdateProfile with an explicit cap value", async () => {
    // feature:managed-profile-policy
    await setProfileStreamCap("p-1", 3);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation UpdateProfile");
    expect(recorded[0].query).toContain(
      "updateProfile(profileID: $profileID, maxConcurrentStreams: $maxConcurrentStreams)",
    );
    expect(recorded[0].variables).toEqual({
      profileID: "p-1",
      maxConcurrentStreams: 3,
    });
  });

  it("setProfileStreamCap clears the cap by sending 0 for a non-positive max", async () => {
    // feature:managed-profile-policy
    await setProfileStreamCap("p-1", 0);
    expect(recorded[0].variables).toEqual({
      profileID: "p-1",
      maxConcurrentStreams: 0,
    });
  });

  it("setExtensionPreauthorization sends SetExtensionPreauthorization(profileID, enabled)", async () => {
    // feature:managed-profile-policy
    await setExtensionPreauthorization("p-1", true);
    expect(recorded).toHaveLength(1);
    expect(recorded[0].query).toContain("mutation SetExtensionPreauthorization");
    expect(recorded[0].query).toContain(
      "setExtensionPreauthorization(profileID: $profileID, enabled: $enabled)",
    );
    expect(recorded[0].variables).toEqual({ profileID: "p-1", enabled: true });
  });

  it("retention mutations bind their variables into GraphQL fields", async () => {
    await createRetentionRule({
      name: "Rule",
      sourceExpr: "collection:old",
      targetLibraryID: "library-archive",
      graceDays: 14,
      revealedAtAuthoring: true,
      enabled: true,
    });
    await updateRetentionRule({ id: "r-1", enabled: false });
    await deleteRetentionRule("r-1");
    await setRetentionCandidateHold("r-1", "m-1", true);

    expect(recorded[0].query).toContain(
      "createRetentionRule(name: $name, sourceExpr: $sourceExpr, targetLibraryID: $targetLibraryID, graceDays: $graceDays, revealedAtAuthoring: $revealedAtAuthoring, enabled: $enabled)",
    );
    expect(recorded[0].variables).toEqual({
      name: "Rule",
      sourceExpr: "collection:old",
      targetLibraryID: "library-archive",
      graceDays: 14,
      revealedAtAuthoring: true,
      enabled: true,
    });
    expect(recorded[1].query).toContain(
      "updateRetentionRule(id: $id, name: $name, sourceExpr: $sourceExpr, targetLibraryID: $targetLibraryID, graceDays: $graceDays, revealedAtAuthoring: $revealedAtAuthoring, enabled: $enabled)",
    );
    expect(recorded[1].variables).toEqual({ id: "r-1", enabled: false });
    expect(recorded[2].query).toContain("deleteRetentionRule(id: $id)");
    expect(recorded[2].variables).toEqual({ id: "r-1" });
    expect(recorded[3].query).toContain(
      "setRetentionCandidateHold(ruleID: $ruleID, mediaID: $mediaID, hold: $hold)",
    );
    expect(recorded[3].variables).toEqual({
      ruleID: "r-1",
      mediaID: "m-1",
      hold: true,
    });
  });
});
