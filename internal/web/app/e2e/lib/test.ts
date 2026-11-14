import { type BrowserContext, test as base } from "@playwright/test";

export * from "@playwright/test";

// The tab flag the viewer sets once someone answers "Who's watching?"
// (src/components/beamfall/profile/entry.ts, CORE-1196). The harness server
// has more than one Profile, so without it every viewer visit opens on the
// picker. Session storage is per tab and Playwright's storageState does not
// carry it, so each context gets it from an init script instead.
const PROFILE_PICKED_KEY = "beamfall.profile-picked";

// Mark a context's tabs as having answered the picker. For contexts a spec
// builds itself with browser.newContext(); the fixture below covers the rest.
export async function markProfilePicked(context: BrowserContext): Promise<void> {
  await context.addInitScript((key) => {
    try {
      window.sessionStorage.setItem(key, "1");
    } catch {
      // Storage blocked: the viewer already lets the visit through.
    }
  }, PROFILE_PICKED_KEY);
}

// Every spec's default context has answered the picker, so specs about other
// pages land where they navigate. A spec about the picker itself opts out
// with test.use({ profilePicked: false }).
export const test = base.extend<{ profilePicked: boolean }>({
  profilePicked: [true, { option: true }],
  context: async ({ context, profilePicked }, use) => {
    if (profilePicked) await markProfilePicked(context);
    await use(context);
  },
});
