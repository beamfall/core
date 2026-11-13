import { beforeEach, describe, expect, it, vi } from "vitest";
import { graphql, postJSON } from "@/lib/api";
import { rebindCompatibility } from "@/lib/compat";
import {
  adminSignIn,
  endAdminSession,
  enroll,
  promote,
  renewSession,
  verifyAdminSession,
} from "./auth";

vi.mock("@/lib/api", () => ({
  graphql: vi.fn(),
  postJSON: vi.fn(),
}));

vi.mock("@/lib/compat", () => ({
  rebindCompatibility: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(graphql).mockReset();
  vi.mocked(postJSON).mockReset();
  vi.mocked(rebindCompatibility).mockReset();
  vi.mocked(rebindCompatibility).mockResolvedValue({
    decision: "compatible",
    messageClass: "compat-compatible",
  });
});

describe("first admin password (CORE-1132)", () => {
  it("sends createFirst only when asked, and otherwise leaves the variable out", async () => {
    vi.mocked(graphql).mockResolvedValue({
      verifyAdminSession: { active: true, expiresAt: "2026-08-02T13:00:00Z" },
    });

    await verifyAdminSession("password");
    await verifyAdminSession("password", undefined, true);

    const calls = vi.mocked(graphql).mock.calls;
    expect(calls[0][1]).toEqual({ password: "password" });
    expect(calls[1][1]).toEqual({ password: "password", createFirst: true });
  });
});

describe("admin-session compatibility epochs", () => {
  it("rebinds after opening the admin session", async () => {
    vi.mocked(graphql).mockResolvedValue({
      verifyAdminSession: { active: true, expiresAt: "2026-08-02T13:00:00Z" },
    });

    const result = await verifyAdminSession("password");

    expect(result.active).toBe(true);
    expect(rebindCompatibility).toHaveBeenCalledOnce();
  });

  it("reports a failed rebind after a successful unlock instead of rejecting", async () => {
    vi.mocked(graphql).mockResolvedValue({
      verifyAdminSession: { active: true, expiresAt: "2026-08-02T13:00:00Z" },
    });
    vi.mocked(rebindCompatibility).mockRejectedValue(new Error("handshake unreachable"));

    const result = await verifyAdminSession("password");

    expect(result.active).toBe(true);
    expect(result.rebind).toBe("rebind-failed");
  });

  it("skips the rebind when the credential was not accepted", async () => {
    vi.mocked(graphql).mockResolvedValue({ verifyAdminSession: { active: false } });

    const result = await verifyAdminSession("password");

    expect(result.active).toBe(false);
    expect(rebindCompatibility).not.toHaveBeenCalled();
  });

  it("rebinds after closing the admin session", async () => {
    vi.mocked(graphql).mockResolvedValue({ endAdminSession: { active: false } });

    const result = await endAdminSession();

    expect(result.active).toBe(false);
    expect(rebindCompatibility).toHaveBeenCalledOnce();
  });
});

describe("web-session compatibility epochs", () => {
  it.each([
    ["enroll", () => enroll("code", "Browser"), "/api/web/enroll"],
    ["promote", () => promote("code"), "/api/web/promote"],
    ["admin sign-in", () => adminSignIn("password", "Browser"), "/api/web/admin-signin"],
    ["session renewal", () => renewSession(), "/api/web/session/renew"],
  ] as const)("rebinds after successful %s", async (_name, mutate, path) => {
    vi.mocked(postJSON).mockResolvedValue(undefined);

    await mutate();

    expect(vi.mocked(postJSON).mock.calls[0]?.slice(0, 2)).toEqual([path, expect.any(Object)]);
    expect(rebindCompatibility).toHaveBeenCalledOnce();
    const mutationOrder = vi.mocked(postJSON).mock.invocationCallOrder[0];
    const rebindOrder = vi.mocked(rebindCompatibility).mock.invocationCallOrder[0];
    expect(mutationOrder).toBeLessThan(rebindOrder);
  });

  it("resolves enrollment with a rebind-failed outcome when the handshake rejects", async () => {
    vi.mocked(postJSON).mockResolvedValue(undefined);
    vi.mocked(rebindCompatibility).mockRejectedValue(new Error("handshake unreachable"));

    await expect(enroll("code", "Browser")).resolves.toBe("rebind-failed");
  });

  it("rejects enrollment without rebinding when the code is refused", async () => {
    vi.mocked(postJSON).mockRejectedValue(new Error("rejected"));

    await expect(enroll("code", "Browser")).rejects.toThrow("rejected");

    expect(rebindCompatibility).not.toHaveBeenCalled();
  });

  it("does not advance compatibility when promotion fails", async () => {
    vi.mocked(postJSON).mockRejectedValue(new Error("rejected"));

    await expect(promote("code")).rejects.toThrow("rejected");

    expect(rebindCompatibility).not.toHaveBeenCalled();
  });

  it("reports a refused renewal as false without rebinding", async () => {
    vi.mocked(postJSON).mockRejectedValue(new Error("refused"));

    await expect(renewSession()).resolves.toBe(false);

    expect(rebindCompatibility).not.toHaveBeenCalled();
  });

  it("rejects admin sign-in without rebinding when the password is refused", async () => {
    vi.mocked(postJSON).mockRejectedValue(new Error("sign-in failed"));

    await expect(adminSignIn("wrong", "Browser")).rejects.toThrow("sign-in failed");

    expect(rebindCompatibility).not.toHaveBeenCalled();
  });
});
