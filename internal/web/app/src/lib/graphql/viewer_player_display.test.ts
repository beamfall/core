import { afterEach, expect, it, vi } from "vitest";
import { commandPlayerDisplay } from "./viewer";

// commandPlayerDisplay against the wire statuses (ADR-0240 D6, CORE-3314).

afterEach(() => vi.unstubAllGlobals());

function replies(...statuses: { status: string; revision: number }[]) {
  const sent: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init?: RequestInit) => {
      sent.push(JSON.parse(init?.body as string).variables);
      const next = statuses.shift();
      return new Response(
        JSON.stringify({
          data: { controlRendererSession: { ...next, sessionID: "s1", command: "pause" } },
        }),
      );
    }),
  );
  return sent;
}

it("retries a CONFLICT once with the server's revision and one commandID", async () => {
  const sent = replies({ status: "CONFLICT", revision: 4 }, { status: "APPLIED", revision: 5 });
  await expect(commandPlayerDisplay({ sessionID: "s1", command: "pause" })).resolves.toMatchObject({
    status: "APPLIED",
    revision: 5,
  });
  expect(sent).toHaveLength(2);
  expect(sent[0].expectedRevision).toBe(0);
  expect(sent[1].expectedRevision).toBe(4);
  expect(sent[1].commandID).toBe(sent[0].commandID);
});

it("throws when the retry is not applied", async () => {
  replies({ status: "CONFLICT", revision: 4 }, { status: "CONFLICT", revision: 6 });
  await expect(commandPlayerDisplay({ sessionID: "s1", command: "pause" })).rejects.toThrow(
    "CONFLICT",
  );
});

it("sends once when the first command applies", async () => {
  const sent = replies({ status: "APPLIED", revision: 1 });
  await commandPlayerDisplay({ sessionID: "s1", command: "stop" });
  expect(sent).toHaveLength(1);
});
