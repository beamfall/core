import { writeFileSync } from "node:fs";

// CORE-0941: the smoke runner's watchdog fails a run that makes no progress, not one that is merely
// slow under host load. This reporter stamps BEAMFALL_WEB_E2E_HEARTBEAT on every test and output
// event so a passing-but-slow run keeps the watchdog quiet while a hung one goes silent. It never
// affects the run's outcome: a failed stamp is ignored.

function stamp() {
  const path = process.env.BEAMFALL_WEB_E2E_HEARTBEAT;
  if (!path) return;
  try {
    writeFileSync(path, `${Date.now()}\n`, { mode: 0o600 });
  } catch {
    // The watchdog then sees no progress, which fails closed.
  }
}

export default class ProgressHeartbeatReporter {
  onBegin() {
    stamp();
  }

  onTestBegin() {
    stamp();
  }

  onTestEnd() {
    stamp();
  }

  onStdOut() {
    stamp();
  }

  onStdErr() {
    stamp();
  }

  printsToStdio() {
    return false;
  }
}
