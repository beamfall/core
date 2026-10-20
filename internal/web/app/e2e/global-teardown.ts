import { existsSync, readFileSync } from "node:fs";
import { STATE_FILE } from "./global-setup";

// A SIGTERMed server is not a stopped server: node exits as soon as the signal
// is delivered, so the booted process reparents to init and is still shutting
// down when the gate's process supervisor takes its inventory. The supervisor
// reaps it and fails the run. Teardown therefore waits for the process to
// actually be gone, and escalates if it is not.
const GRACEFUL_EXIT_TIMEOUT_MS = 15_000;
const FORCED_EXIT_TIMEOUT_MS = 5_000;
const POLL_INTERVAL_MS = 50;

function processIsAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function signalProcess(pid: number, signal: NodeJS.Signals): void {
  try {
    process.kill(pid, signal);
  } catch {
    // already gone
  }
}

async function waitForExit(pid: number, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (!processIsAlive(pid)) return true;
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
  }
  return !processIsAlive(pid);
}

// Stop the server global-setup booted (if any). An externally-provided
// BEAMFALL_E2E_URL server has no recorded pid and is left untouched.
export default async function globalTeardown(): Promise<void> {
  if (!existsSync(STATE_FILE)) return;
  let pid: number | undefined;
  try {
    const state = JSON.parse(readFileSync(STATE_FILE, "utf8")) as {
      pid?: number;
    };
    pid = state.pid;
  } catch (cause) {
    // Fail closed. The state file exists, so global-setup got far enough to
    // write it; if it cannot be read we may be abandoning a booted server.
    // Swallowing this is what lets a leaked server reach the gate supervisor.
    throw new Error(
      `e2e teardown: ${STATE_FILE} exists but is unreadable; a booted server may be leaking`,
      {
        cause,
      },
    );
  }
  if (!pid) return;

  signalProcess(pid, "SIGTERM");
  if (await waitForExit(pid, GRACEFUL_EXIT_TIMEOUT_MS)) return;

  signalProcess(pid, "SIGKILL");
  if (await waitForExit(pid, FORCED_EXIT_TIMEOUT_MS)) return;

  throw new Error(
    `e2e teardown: booted server pid ${pid} survived SIGTERM and SIGKILL; ` +
      "it would be reaped as a residual process by the gate supervisor",
  );
}
