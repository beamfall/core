import { existsSync, readFileSync } from "node:fs";
import { join, sep } from "node:path";
import { chromium } from "@playwright/test";

const app = join(import.meta.dirname, "..", "..");
const lock = JSON.parse(readFileSync(join(app, "package-lock.json"), "utf8"));
const expectedVersion = lock.packages?.["node_modules/@playwright/test"]?.version;
const installedVersion = JSON.parse(
  readFileSync(join(app, "node_modules", "@playwright", "test", "package.json"), "utf8"),
).version;

if (typeof expectedVersion !== "string" || expectedVersion.length === 0) {
  throw new Error("package-lock.json does not pin @playwright/test");
}
if (installedVersion !== expectedVersion) {
  throw new Error(
    `installed Playwright ${installedVersion} differs from lockfile ${expectedVersion}`,
  );
}
if (process.argv.includes("--package-only")) {
  console.log(`PASS pinned Playwright package version=${expectedVersion}`);
  process.exit(0);
}

const registry = JSON.parse(
  readFileSync(join(app, "node_modules", "playwright-core", "browsers.json"), "utf8"),
);
const chromiumEntry = registry.browsers?.find((browser) => browser.name === "chromium");
if (!chromiumEntry?.revision) throw new Error("Playwright registry does not pin Chromium");

const executable = chromium.executablePath();
if (!existsSync(executable)) throw new Error(`Chromium ${chromiumEntry.revision} is not installed`);
if (!executable.split(sep).includes(`chromium-${chromiumEntry.revision}`)) {
  throw new Error(
    `Chromium executable ${executable} does not match registry revision ${chromiumEntry.revision}`,
  );
}

console.log(
  `PASS pinned Chromium playwright=${expectedVersion} revision=${chromiumEntry.revision}`,
);
