import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { FullConfig } from "@playwright/test";
import {
  ADMIN_PASSWORD,
  createSeedAdultProfile,
  designateSeedOwner,
  enrollRole,
  openAdminSession,
  storageStateFor,
  switchEnrolledProfile,
} from "./lib/harness";

// Where the per-role storageStates and the harness manifest land. playwright
// projects load these; specs read state.json to learn the base URL and whether
// the Console (admin) lane is available.
// Nested under .auth/ so the existing `e2e/.auth/` gitignore still covers it.
export const AUTH_DIR = join(
  import.meta.dirname,
  ".auth",
  String(process.env.BEAMFALL_E2E_PORT ?? 8830),
);
export const STATE_FILE = join(AUTH_DIR, "state.json");
export const VIEWER_STATE = join(AUTH_DIR, "viewer.json");
export const MANAGER_STATE = join(AUTH_DIR, "manager.json");
export const CONSOLE_STATE = join(AUTH_DIR, "console.json");
export const MANAGED_VIEWER_STATE = join(AUTH_DIR, "managed-viewer.json");
export const MANAGED_MANAGER_STATE = join(AUTH_DIR, "managed-manager.json");
export const NON_OWNER_VIEWER_STATE = join(AUTH_DIR, "non-owner-viewer.json");

// The default self-booted server port. Chosen distinct from the orchestrator's
// 8810 so the acceptance gate is self-contained and never collides.
// Concurrent gates in sibling worktrees each boot their own server, so a fixed
// port makes them collide: one run binds, the next false-fails and loses its
// server to the gate supervisor. The runner picks a free port and exports it.
const E2E_PORT = Number(process.env.BEAMFALL_E2E_PORT ?? 8830);
// The gate builds the server binary FRESH from source (go build) so it always
// embeds the CURRENT internal/web/app-dist bundle. This is deliberate: a stale
// prebuilt binary embedding an old web bundle silently breaks role/console
// behavior (the bundle is go:embed'd, not served from disk), which is exactly
// the runtime-vs-typecheck gap this gate exists to catch. The repo root is four
// levels up from this file (internal/web/app/e2e).
const REPO_ROOT = join(import.meta.dirname, "..", "..", "..", "..");
// Per-port too: two concurrent runs both `go build -o` this path, and a rebuild
// landing on the binary a sibling run is still launching kills that server —
// the surviving symptom is ECONNREFUSED partway through the suite.
const SERVER_BIN = `/tmp/beamfall-e2e-${process.env.BEAMFALL_E2E_PORT ?? 8830}`;

interface HarnessState {
  baseURL: string;
  adminAvailable: boolean;
  selfBooted: boolean;
  pid?: number;
  dataDir?: string;
}

const smokeGate = process.env.BEAMFALL_WEB_E2E_SMOKE_GATE === "1";

async function waitForInterruptProbe(boot: { pid: number; dataDir: string }): Promise<void> {
  const readyFile = process.env.BEAMFALL_E2E_INTERRUPT_READY_FILE;
  if (!readyFile) return;

  writeFileSync(readyFile, JSON.stringify({ ...boot, runnerPid: process.pid }));
  await new Promise<never>((_resolve, reject) => {
    const keepAlive = setInterval(() => {}, 1_000);
    process.once("SIGINT", () => {
      clearInterval(keepAlive);
      reject(new Error("[e2e] setup interrupted by descendant-cleanup regression probe"));
    });
  });
}

async function waitForServer(
  baseURL: string,
  timeoutMs: number,
  abortReason?: () => string | undefined,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const reason = abortReason?.();
    if (reason) throw new Error(reason);
    try {
      const res = await fetch(`${baseURL}/web/`, { redirect: "manual" });
      if (res.status > 0) return true;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  const reason = abortReason?.();
  if (reason) throw new Error(reason);
  return false;
}

// Build the server binary fresh from source so it embeds the current web bundle.
// Throws with the compiler output if the build fails.
function buildServer(): void {
  // eslint-disable-next-line no-console
  console.log("[e2e] building embedded web app (npm run build)...");
  const webResult = spawnSync("npm", ["--prefix", "internal/web/app", "run", "build"], {
    cwd: REPO_ROOT,
    encoding: "utf8",
  });
  if (webResult.status !== 0) {
    throw new Error(
      `npm run build failed (status ${webResult.status}):\n${webResult.stderr || webResult.stdout}`,
    );
  }
  // eslint-disable-next-line no-console
  console.log(`[e2e] building ${SERVER_BIN} from source (go build ./cmd/beamfall)...`);
  const result = spawnSync("go", ["build", "-o", SERVER_BIN, "./cmd/beamfall"], {
    cwd: REPO_ROOT,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    throw new Error(
      `go build failed (status ${result.status}):\n${result.stderr || result.stdout}`,
    );
  }
}

// Boot a fresh server with a known admin password and dev seed. Returns the pid
// and data dir for teardown. Throws if it never binds.
async function bootServer(baseURL: string): Promise<{ pid: number; dataDir: string }> {
  if (smokeGate && (await waitForServer(baseURL, 500))) {
    throw new Error(
      `[e2e] smoke gate refuses to reuse an existing server at ${baseURL}; ` +
        "stop that process or choose a free E2E_PORT before running make web-e2e",
    );
  }
  buildServer();
  if (!existsSync(SERVER_BIN)) {
    throw new Error(`server binary ${SERVER_BIN} missing after build`);
  }
  const dataDir = mkdtempSync(join(tmpdir(), "beamfall-e2e-"));
  const addr = baseURL.replace(/^https?:\/\//, "");
  const child = spawn(
    SERVER_BIN,
    ["serve", "-addr", addr, "-data-dir", dataDir, "-seed", "development"],
    {
      // BEAMFALL_ADMIN_PASSWORD is applied first-run only (app/secrets.go), so
      // the fresh data dir gets exactly this admin password — the Console lane.
      env: { ...process.env, BEAMFALL_ADMIN_PASSWORD: ADMIN_PASSWORD },
      stdio: "ignore",
      detached: false,
    },
  );
  child.unref();
  let childExit: string | undefined;
  child.once("exit", (code, signal) => {
    childExit = `self-booted server exited before readiness (code=${code}, signal=${signal})`;
  });
  const up = await waitForServer(baseURL, 30_000, () => childExit);
  if (!up) {
    try {
      child.kill("SIGKILL");
    } catch {
      /* ignore */
    }
    throw new Error(`self-booted server never came up at ${baseURL}`);
  }
  return { pid: child.pid!, dataDir };
}

export default async function globalSetup(_config: FullConfig): Promise<void> {
  mkdirSync(AUTH_DIR, { recursive: true });

  // Reuse an externally-provided server when BEAMFALL_E2E_URL is set; otherwise
  // boot our own with the admin password so the Console lane is exercised.
  const externalURL = process.env.BEAMFALL_E2E_URL;
  if (smokeGate && externalURL) {
    throw new Error(
      "BEAMFALL_WEB_E2E_SMOKE_GATE=1 requires the self-booted harness; " + "unset BEAMFALL_E2E_URL",
    );
  }
  const baseURL = externalURL ?? `http://127.0.0.1:${E2E_PORT}`;

  let boot: { pid: number; dataDir: string } | undefined;
  if (!externalURL) {
    boot = await bootServer(baseURL);
    // Record the pid BEFORE the enrollment work below. Anything after this can
    // throw, and until the pid is on disk teardown has nothing to stop — the
    // server would survive the run and be reaped as a residual process.
    writeFileSync(
      STATE_FILE,
      JSON.stringify({ baseURL, adminAvailable: false, selfBooted: true, ...boot }),
    );
    await waitForInterruptProbe(boot);
    await designateSeedOwner(baseURL);
  } else {
    const up = await waitForServer(baseURL, 10_000);
    if (!up) throw new Error(`BEAMFALL_E2E_URL=${baseURL} is not reachable`);
  }

  // Mint two real enrolled browser devices via the faithful pairing flow.
  const viewer = await enrollRole(baseURL, "viewer");
  const manager = await enrollRole(baseURL, "manager");

  writeFileSync(VIEWER_STATE, JSON.stringify(storageStateFor(baseURL, viewer)));
  writeFileSync(MANAGER_STATE, JSON.stringify(storageStateFor(baseURL, manager)));

  if (!externalURL) {
    const nonOwnerPIN = "2468";
    const nonOwnerProfileID = await createSeedAdultProfile(baseURL, "Non Owner", nonOwnerPIN);
    const managedViewer = await enrollRole(baseURL, "viewer");
    await switchEnrolledProfile(baseURL, managedViewer, "profile-managed");
    const managedManager = await enrollRole(baseURL, "manager");
    await switchEnrolledProfile(baseURL, managedManager, "profile-managed");
    const nonOwnerViewer = await enrollRole(baseURL, "viewer");
    await switchEnrolledProfile(baseURL, nonOwnerViewer, nonOwnerProfileID, nonOwnerPIN);

    writeFileSync(MANAGED_VIEWER_STATE, JSON.stringify(storageStateFor(baseURL, managedViewer)));
    writeFileSync(MANAGED_MANAGER_STATE, JSON.stringify(storageStateFor(baseURL, managedManager)));
    writeFileSync(NON_OWNER_VIEWER_STATE, JSON.stringify(storageStateFor(baseURL, nonOwnerViewer)));
  }

  // The Console storageState is the manager browser with an Admin Session opened
  // server-side. It only works when an admin password exists — true for our
  // self-booted server, unknown for an external one (e.g. the orchestrator's
  // 8810 was started without BEAMFALL_ADMIN_PASSWORD). Probe it and record the
  // verdict so console.spec.ts can skip gracefully when admin is unavailable.
  const consoleBrowser = await enrollRole(baseURL, "manager");
  const adminAvailable = await openAdminSession(baseURL, consoleBrowser);
  if (smokeGate && !adminAvailable) {
    throw new Error("WEB E2E smoke gate requires an unlocked Console Admin Session");
  }
  writeFileSync(CONSOLE_STATE, JSON.stringify(storageStateFor(baseURL, consoleBrowser)));

  const state: HarnessState = {
    baseURL,
    adminAvailable,
    selfBooted: !externalURL,
    pid: boot?.pid,
    dataDir: boot?.dataDir,
  };
  writeFileSync(STATE_FILE, JSON.stringify(state));

  // eslint-disable-next-line no-console
  console.log(
    `[e2e] harness ready: base=${baseURL} self-booted=${!externalURL} ` +
      `adminAvailable=${adminAvailable}`,
  );
}
