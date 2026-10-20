// Shared E2E harness constants and the faithful enrollment flow.
//
// The embedded React app authenticates entirely through three HttpOnly cookies
// (beamfall_session / beamfall_device / beamfall_device_secret) plus a constant
// CSRF header on mutations (internal/auth/auth.go, internal/web/app/src/lib/api.ts).
// A fresh browser has none, so global-setup mints them through the REAL ADR-0012
// §5 path and hands Playwright a storageState per role:
//
//   1. Mint a pairing code with the seeded MANAGER device credential, sent via the
//      X-Beamfall-* header lane (the lane the conformance test and native apps use).
//      The dev seed (internal/store/sqlite.go developmentSeed) creates device-mac
//      (DeviceRoleManager) + session-manager; HashDeviceSecret is plain SHA-256, so
//      the plaintext secret "dev-manager-secret" verifies against the seeded hash.
//   2. POST /api/web/enroll with that code → the server sets Set-Cookie for a fresh
//      browser device whose role matches the code (viewer | manager). Those cookies
//      are exactly what the cookie credential path validates.
//
// This is faithful, not injected: every storageState is a real enrolled browser
// device the server minted, with the role it actually grants.

export const ADMIN_PASSWORD = "e2e-admin-pw";

// The seeded MANAGER credential (internal/store/sqlite.go developmentSeed +
// store.HashDeviceSecret): device-mac is DeviceRoleManager; "dev-manager-secret"
// hashes to its seeded SecretHash. This is the only seeded affordance that can
// mint a pairing code (pairing.CreateCode requires DeviceRoleManager).
const SEED_MANAGER = {
  session: "session-manager",
  device: "device-mac",
  secret: "dev-manager-secret",
} as const;

const CSRF_HEADER = "X-Beamfall-Csrf";

export interface EnrolledCookies {
  session: string;
  device: string;
  deviceSecret: string;
}

// Mint a one-time pairing code for the given role using the seeded manager
// header credential. Throws with the server's body on any non-201.
async function mintPairingCode(baseURL: string, deviceRole: "viewer" | "manager"): Promise<string> {
  const res = await fetch(`${baseURL}/api/pairing-codes`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [CSRF_HEADER]: "1",
      "X-Beamfall-Session": SEED_MANAGER.session,
      "X-Beamfall-Device": SEED_MANAGER.device,
      "X-Beamfall-Device-Secret": SEED_MANAGER.secret,
    },
    body: JSON.stringify({ deviceRole }),
  });
  const body = await res.text();
  if (res.status !== 201) {
    throw new Error(`mint pairing code (${deviceRole}) failed: ${res.status} ${body}`);
  }
  return JSON.parse(body).code as string;
}

// Enroll a fresh browser device with a pairing code and return the three
// credential cookies the server set. Reads Set-Cookie directly because the
// HttpOnly cookies are not JS-readable in a real browser, but the enroll
// response carries them on the wire here.
async function enrollBrowser(
  baseURL: string,
  code: string,
  deviceName: string,
): Promise<EnrolledCookies> {
  const res = await fetch(`${baseURL}/api/web/enroll`, {
    method: "POST",
    headers: { "Content-Type": "application/json", [CSRF_HEADER]: "1" },
    body: JSON.stringify({ code, deviceName }),
  });
  const body = await res.text();
  if (res.status !== 201) {
    throw new Error(`enroll (${deviceName}) failed: ${res.status} ${body}`);
  }
  // node fetch exposes the combined Set-Cookie via getSetCookie() (undici).
  const setCookies = (res.headers as unknown as { getSetCookie(): string[] }).getSetCookie();
  const pick = (name: string): string => {
    const header = setCookies.find((c) => c.startsWith(`${name}=`));
    if (!header) throw new Error(`enroll response missing ${name} cookie`);
    return header.slice(name.length + 1).split(";")[0];
  };
  return {
    session: pick("beamfall_session"),
    device: pick("beamfall_device"),
    deviceSecret: pick("beamfall_device_secret"),
  };
}

// Mint a fresh enrolled browser device of the given role, end to end.
export async function enrollRole(
  baseURL: string,
  role: "viewer" | "manager",
): Promise<EnrolledCookies> {
  const code = await mintPairingCode(baseURL, role);
  return enrollBrowser(baseURL, code, `E2E ${role}`);
}

// Open an Admin Session for an already-enrolled MANAGER browser by verifying the
// admin password through GraphQL with that browser's cookies. The window lives
// server-side on the session row (auth.go BeginAdminSession; 15-min TTL), so the
// Console renders unlocked for the whole run as long as the same cookies ride.
// Returns true if the Admin Session is active afterward.
export async function openAdminSession(
  baseURL: string,
  cookies: EnrolledCookies,
): Promise<boolean> {
  const cookieHeader = [
    `beamfall_session=${cookies.session}`,
    `beamfall_device=${cookies.device}`,
    `beamfall_device_secret=${cookies.deviceSecret}`,
  ].join("; ");
  const res = await fetch(`${baseURL}/api/graphql`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [CSRF_HEADER]: "1",
      Cookie: cookieHeader,
    },
    body: JSON.stringify({
      query:
        "mutation V($password: String!) { verifyAdminSession(password: $password) { active } }",
      variables: { password: ADMIN_PASSWORD },
    }),
  });
  const body = await res.json();
  return body?.data?.verifyAdminSession?.active === true;
}

export async function graphqlWithSeedManager(
  baseURL: string,
  query: string,
  variables: Record<string, unknown>,
): Promise<unknown> {
  const res = await fetch(`${baseURL}/api/graphql`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [CSRF_HEADER]: "1",
      "X-Beamfall-Session": SEED_MANAGER.session,
      "X-Beamfall-Device": SEED_MANAGER.device,
      "X-Beamfall-Device-Secret": SEED_MANAGER.secret,
    },
    body: JSON.stringify({ query, variables }),
  });
  const body = await res.json();
  if (!res.ok || body?.errors?.length) {
    throw new Error(`seed manager GraphQL failed: ${res.status} ${JSON.stringify(body)}`);
  }
  return body.data;
}

export async function designateSeedOwner(baseURL: string): Promise<void> {
  const admin = (await graphqlWithSeedManager(
    baseURL,
    "mutation V($password: String!) { verifyAdminSession(password: $password) { active } }",
    { password: ADMIN_PASSWORD },
  )) as { verifyAdminSession?: { active?: boolean } };
  if (admin.verifyAdminSession?.active !== true) {
    throw new Error("seed manager could not open the bootstrap Admin Session");
  }

  const owner = (await graphqlWithSeedManager(
    baseURL,
    "mutation O($profileID: String!) { transferProfileOwner(profileID: $profileID) { profileID isOwner } }",
    { profileID: "profile-default" },
  )) as { transferProfileOwner?: { profileID?: string; isOwner?: boolean } };
  if (
    owner.transferProfileOwner?.profileID !== "profile-default" ||
    owner.transferProfileOwner?.isOwner !== true
  ) {
    throw new Error(`seed owner designation failed: ${JSON.stringify(owner.transferProfileOwner)}`);
  }
}

export async function createSeedAdultProfile(
  baseURL: string,
  name: string,
  entryPIN: string,
): Promise<string> {
  const data = (await graphqlWithSeedManager(
    baseURL,
    "mutation C($name: String!, $kind: String!) { createProfile(name: $name, kind: $kind) { id kind } }",
    { name, kind: "adult" },
  )) as { createProfile?: { id?: string; kind?: string } };
  const profile = data.createProfile;
  if (!profile?.id || profile.kind !== "adult") {
    throw new Error(`seed adult profile creation failed: ${JSON.stringify(profile)}`);
  }
  await graphqlWithSeedManager(
    baseURL,
    "mutation P($profileID: String!, $entryPIN: String!) { updateProfile(profileID: $profileID, entryPIN: $entryPIN) { id } }",
    { profileID: profile.id, entryPIN },
  );
  return profile.id;
}

export async function switchEnrolledProfile(
  baseURL: string,
  cookies: EnrolledCookies,
  profileID: string,
  pin = "",
): Promise<void> {
  const cookieHeader = [
    `beamfall_session=${cookies.session}`,
    `beamfall_device=${cookies.device}`,
    `beamfall_device_secret=${cookies.deviceSecret}`,
  ].join("; ");
  const res = await fetch(`${baseURL}/api/graphql`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      [CSRF_HEADER]: "1",
      Cookie: cookieHeader,
    },
    body: JSON.stringify({
      query:
        "mutation S($profileID: String!, $pin: String) { switchProfile(profileID: $profileID, pin: $pin) { profileID revealed } }",
      variables: { profileID, pin },
    }),
  });
  const body = await res.json();
  if (!res.ok || body?.errors?.length) {
    throw new Error(`switch enrolled profile failed: ${res.status} ${JSON.stringify(body)}`);
  }
  if (body?.data?.switchProfile?.profileID !== profileID) {
    throw new Error(
      `switch enrolled profile returned ${JSON.stringify(body?.data?.switchProfile)}`,
    );
  }
}

// Build a Playwright storageState from enrolled cookies. The cookies are marked
// secure (the server sets Secure on all three); Chromium treats http://127.0.0.1
// as a secure context, so secure cookies are accepted and replayed there.
export function storageStateFor(baseURL: string, cookies: EnrolledCookies) {
  const url = new URL(baseURL);
  const domain = url.hostname;
  const common = {
    domain,
    path: "/",
    httpOnly: true,
    // Mirror what the server actually sets (internal/server/web_test.go: plain
    // HTTP gets HttpOnly + Strict but NOT Secure). WebKit drops Secure cookies
    // on an http:// origin — including loopback — so an unconditional `true`
    // silently unenrolls the seeded browser there.
    secure: url.protocol === "https:",
    sameSite: "Strict" as const,
    expires: -1,
  };
  return {
    cookies: [
      { name: "beamfall_session", value: cookies.session, ...common },
      { name: "beamfall_device", value: cookies.device, ...common },
      { name: "beamfall_device_secret", value: cookies.deviceSecret, ...common },
    ],
    origins: [],
  };
}
